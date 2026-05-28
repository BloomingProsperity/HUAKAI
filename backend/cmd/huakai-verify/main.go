package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, http.DefaultClient))
}

func runCLI(args []string, out io.Writer, client *http.Client) int {
	fs := flag.NewFlagSet("huakai-verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pubkeyURL := fs.String("pubkey-url", "", "public ed25519 key URL")
	requestID := fs.String("request-id", "", "HUAKAI request_id")
	tenantScopeRef := fs.String("tenant-scope-ref", "", "HUAKAI tenant_scope_ref from X-HUAKAI-Verify")
	gatewayURL := fs.String("gateway-url", "", "HUAKAI gateway base URL")
	receiptFile := fs.String("receipt-file", "", "canonical trust receipt file, or '-' for stdin")
	signature := fs.String("signature", "", "base64 detached ed25519 signature")
	server := fs.String("server", "", "HUAKAI gateway base URL for .well-known/huakai-pubkey.json")
	fingerprint := fs.String("fingerprint", "", "expected public key fingerprint")
	refresh := fs.Bool("refresh", false, "refresh cached public keys before verifying")
	jsonOutput := fs.Bool("json", false, "write machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(out, "audit verification failed: %v\n", err)
		return 2
	}
	if *receiptFile != "" || *signature != "" {
		return runDetachedCLI(detachedOptions{
			ReceiptFile: *receiptFile,
			Signature:   *signature,
			Server:      *server,
			Fingerprint: *fingerprint,
			Refresh:     *refresh,
			JSON:        *jsonOutput,
		}, out, client)
	}
	if *pubkeyURL == "" || *requestID == "" || *tenantScopeRef == "" || *gatewayURL == "" {
		fmt.Fprintln(out, "audit verification failed: --pubkey-url, --request-id, --tenant-scope-ref, and --gateway-url are required")
		return 2
	}
	if client == nil {
		client = http.DefaultClient
	}

	verify, tree, err := fetchAudit(client, *gatewayURL, *requestID, *tenantScopeRef)
	if err != nil {
		fmt.Fprintf(out, "audit verification failed: %v\n", err)
		return 1
	}
	pub, err := fetchPubKey(client, *pubkeyURL, verify.ChainProof.PubkeyFingerprint)
	if err != nil {
		fmt.Fprintf(out, "audit verification failed: %v\n", err)
		return 1
	}
	entry, err := verifyEntryProof(verify, pub)
	if err != nil {
		fmt.Fprintf(out, "audit verification failed: %v\n", err)
		return 1
	}
	if err := verifyRequestedEntry(entry, *requestID, *tenantScopeRef); err != nil {
		fmt.Fprintf(out, "audit verification failed: %v\n", err)
		return 1
	}

	fmt.Fprintln(out, "audit verification passed")
	fmt.Fprintf(out, "request_id=%s ledger_id=%s\n", entry.RequestID, entry.LedgerID)
	fmt.Fprintf(out, "signature verified with pubkey_fingerprint=%s\n", entry.PubkeyFingerprint)
	fmt.Fprintf(out, "chain proof verified: prev_merkle_root=%x merkle_root=%x\n", entry.PrevMerkleRoot, entry.MerkleRoot)
	fmt.Fprintf(out, "public ledger: latest_merkle_root=%s size=%d\n", tree.LatestMerkleRoot, tree.Size)
	if tree.LatestMerkleRoot == verify.ChainProof.MerkleRoot {
		fmt.Fprintln(out, "entry merkle_root matches the current public chain tip")
	} else {
		fmt.Fprintln(out, "entry root is signed locally; current public tip differs, so later entries may exist")
	}
	return 0
}

type detachedOptions struct {
	ReceiptFile string
	Signature   string
	Server      string
	Fingerprint string
	Refresh     bool
	JSON        bool
}

type detachedResult struct {
	Valid          bool   `json:"valid"`
	Status         string `json:"status"`
	SignatureValid bool   `json:"signature_valid"`
	KeyStatus      string `json:"key_status"`
	Reason         string `json:"reason,omitempty"`
	CanonicalHash  string `json:"canonical_hash"`
	Fingerprint    string `json:"pubkey_fingerprint"`
	CachePath      string `json:"cache_path,omitempty"`
	CacheRefreshed bool   `json:"cache_refreshed"`
}

func runDetachedCLI(opts detachedOptions, out io.Writer, client *http.Client) int {
	if client == nil {
		client = http.DefaultClient
	}
	payload, err := readDetachedPayload(opts.ReceiptFile)
	if err != nil {
		return writeDetachedError(out, opts.JSON, "payload_read_failed", err)
	}
	if strings.TrimSpace(opts.Signature) == "" {
		return writeDetachedError(out, opts.JSON, "signature_missing", errors.New("--signature is required for detached verification"))
	}
	sig, err := decodeFlexibleBase64(opts.Signature)
	if err != nil {
		return writeDetachedError(out, opts.JSON, "signature_invalid", err)
	}
	server := strings.TrimSpace(opts.Server)
	if server == "" {
		server = defaultVerifyServer()
	}
	keys, cachePath, refreshed, err := loadKnownKeys(client, server, opts.Refresh)
	if err != nil {
		return writeDetachedError(out, opts.JSON, "pubkey_fetch_failed", err)
	}
	record, err := selectPubkeyRecord(keys, strings.TrimSpace(opts.Fingerprint))
	if err != nil {
		return writeDetachedError(out, opts.JSON, "pubkey_not_found", err)
	}
	pub, err := decodePubKeyMaterial(record.Material)
	if err != nil {
		return writeDetachedError(out, opts.JSON, "pubkey_invalid", err)
	}
	computedFP := sign.Fingerprint(pub)
	if computedFP != record.Fingerprint {
		return writeDetachedError(out, opts.JSON, "fingerprint_mismatch", fmt.Errorf("fingerprint mismatch: got %s want %s", computedFP, record.Fingerprint))
	}
	if opts.Fingerprint != "" && computedFP != strings.TrimSpace(opts.Fingerprint) {
		return writeDetachedError(out, opts.JSON, "fingerprint_mismatch", fmt.Errorf("fingerprint mismatch: got %s want %s", computedFP, strings.TrimSpace(opts.Fingerprint)))
	}
	sum := sha256.Sum256(payload)
	result := detachedResult{
		Valid:          false,
		Status:         "mismatch",
		SignatureValid: false,
		KeyStatus:      record.Status,
		CanonicalHash:  hex.EncodeToString(sum[:]),
		Fingerprint:    computedFP,
		CachePath:      cachePath,
		CacheRefreshed: refreshed,
	}
	if result.KeyStatus == "" {
		result.KeyStatus = "active"
	}
	if record.Revoked {
		result.Status = "unverified"
		result.SignatureValid = sign.Verify(pub, payload, sig) == nil
		result.KeyStatus = "revoked"
		result.Reason = "key_revoked"
		writeDetachedResult(out, opts.JSON, result)
		return 1
	}
	if sign.Verify(pub, payload, sig) != nil {
		result.Reason = "signature_mismatch"
		writeDetachedResult(out, opts.JSON, result)
		return 1
	}
	result.Valid = true
	result.Status = "signed-only"
	result.SignatureValid = true
	writeDetachedResult(out, opts.JSON, result)
	return 0
}

func readDetachedPayload(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func defaultVerifyServer() string {
	if server := strings.TrimSpace(os.Getenv("HUAKAI_VERIFY_SERVER")); server != "" {
		return server
	}
	return "https://gateway.example.com"
}

func loadKnownKeys(client *http.Client, server string, refresh bool) ([]byte, string, bool, error) {
	cachePath, err := knownKeysPath(server)
	if err != nil {
		return nil, "", false, err
	}
	if !refresh {
		if cached, err := os.ReadFile(cachePath); err == nil && len(strings.TrimSpace(string(cached))) > 0 {
			return cached, cachePath, false, nil
		}
	}
	target := strings.TrimRight(server, "/") + "/.well-known/huakai-pubkey.json"
	res, err := client.Get(target)
	if err != nil {
		return nil, cachePath, true, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, cachePath, true, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, cachePath, true, fmt.Errorf("%s returned HTTP %d: %s", target, res.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return nil, cachePath, true, err
	}
	if err := os.WriteFile(cachePath, body, 0o600); err != nil {
		return nil, cachePath, true, err
	}
	return body, cachePath, true, nil
}

func knownKeysPath(server string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(server))
	if err != nil {
		return "", err
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("server host required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".huakai", "known_keys", host+".json"), nil
}

type pubkeyRecord struct {
	Fingerprint string
	Material    string
	Status      string
	Revoked     bool
}

func selectPubkeyRecord(body []byte, expectedFP string) (pubkeyRecord, error) {
	var doc pubkeyDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return pubkeyRecord{}, err
	}
	records := pubkeyRecordsFromDoc(doc, revokedFingerprintSet(doc))
	if expectedFP == "" {
		expectedFP = strings.TrimSpace(doc.Current)
	}
	if expectedFP == "" && len(records) == 1 {
		expectedFP = records[0].Fingerprint
	}
	if expectedFP == "" {
		return pubkeyRecord{}, errors.New("fingerprint required when well-known document has no current key")
	}
	for _, record := range records {
		if record.Fingerprint == expectedFP {
			return record, nil
		}
	}
	if expectedFP != "" && len(records) == 1 {
		return records[0], nil
	}
	return pubkeyRecord{}, fmt.Errorf("pubkey fingerprint %s not found", expectedFP)
}

func pubkeyRecordsFromDoc(doc pubkeyDoc, revoked map[string]bool) []pubkeyRecord {
	var out []pubkeyRecord
	fp := fingerprintFromDoc(doc)
	material := materialFromDoc(doc)
	if material != "" {
		out = append(out, pubkeyRecord{
			Fingerprint: fp,
			Material:    material,
			Status:      keyStatusFromDoc(doc),
			Revoked:     keyStatusFromDoc(doc) == "revoked" || revoked[fp],
		})
	}
	for _, key := range doc.Keys {
		out = append(out, pubkeyRecordsFromDoc(key, revoked)...)
	}
	return out
}

func revokedFingerprintSet(doc pubkeyDoc) map[string]bool {
	out := map[string]bool{}
	for _, rev := range doc.Revoked {
		if fp := fingerprintFromDoc(rev); fp != "" {
			out[fp] = true
		}
	}
	return out
}

func writeDetachedResult(out io.Writer, asJSON bool, result detachedResult) {
	if asJSON {
		_ = json.NewEncoder(out).Encode(result)
		return
	}
	fmt.Fprintf(out, "签名状态: %s\n", result.Status)
	fmt.Fprintf(out, "signature_valid=%t key_status=%s\n", result.SignatureValid, result.KeyStatus)
	if result.Reason != "" {
		fmt.Fprintf(out, "reason=%s\n", result.Reason)
	}
	fmt.Fprintf(out, "pubkey_fingerprint=%s\n", result.Fingerprint)
	fmt.Fprintf(out, "canonical_hash=%s\n", result.CanonicalHash)
	if result.CacheRefreshed {
		fmt.Fprintf(out, "首次信任: 已缓存公钥到 %s\n", result.CachePath)
	}
}

func writeDetachedError(out io.Writer, asJSON bool, code string, err error) int {
	if asJSON {
		_ = json.NewEncoder(out).Encode(map[string]any{"valid": false, "status": "mismatch", "reason": code, "error": err.Error()})
		return 1
	}
	fmt.Fprintf(out, "签名验证失败: %s: %v\n", code, err)
	return 1
}

func fetchAudit(client *http.Client, gatewayURL, requestID, tenantScopeRef string) (gatewayhttp.AuditVerifyResponse, gatewayhttp.AuditMerkleTreeResponse, error) {
	var verify gatewayhttp.AuditVerifyResponse
	verifyQuery := url.Values{}
	verifyQuery.Set("request_id", requestID)
	verifyQuery.Set("tenant_scope_ref", tenantScopeRef)
	verifyURL := strings.TrimRight(gatewayURL, "/") + "/v1/audit/verify?" + verifyQuery.Encode()
	if err := fetchJSON(client, verifyURL, &verify); err != nil {
		return verify, gatewayhttp.AuditMerkleTreeResponse{}, err
	}
	var tree gatewayhttp.AuditMerkleTreeResponse
	treeURL := strings.TrimRight(gatewayURL, "/") + "/v1/audit/merkle-tree.json"
	if err := fetchJSON(client, treeURL, &tree); err != nil {
		return verify, tree, err
	}
	if tree.Size == 0 {
		return verify, tree, errors.New("public audit merkle tree is empty")
	}
	return verify, tree, nil
}

func verifyEntryProof(resp gatewayhttp.AuditVerifyResponse, pub ed25519.PublicKey) (auditledger.LedgerEntry, error) {
	entry, err := ledgerEntryFromResponse(resp)
	if err != nil {
		return auditledger.LedgerEntry{}, err
	}
	if got := sign.Fingerprint(pub); got != entry.PubkeyFingerprint {
		return auditledger.LedgerEntry{}, fmt.Errorf("pubkey fingerprint mismatch: got %s want %s", got, entry.PubkeyFingerprint)
	}
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		return auditledger.LedgerEntry{}, fmt.Errorf("signature base64 decode: %w", err)
	}
	hash, err := auditledger.EntryHash(&entry)
	if err != nil {
		return auditledger.LedgerEntry{}, fmt.Errorf("entry hash: %w", err)
	}
	if err := sign.Verify(pub, hash[:], sig); err != nil {
		return auditledger.LedgerEntry{}, err
	}
	if want := auditledger.NextMerkleRoot(entry.PrevMerkleRoot, hash); want != entry.MerkleRoot {
		return auditledger.LedgerEntry{}, errors.New("merkle root does not match prev_merkle_root + entry_hash")
	}
	return entry, nil
}

func verifyRequestedEntry(entry auditledger.LedgerEntry, requestID, tenantScopeRef string) error {
	if entry.RequestID != requestID {
		return fmt.Errorf("response request_id mismatch: got %q want %q", entry.RequestID, requestID)
	}
	if entry.TenantScopeRef != tenantScopeRef {
		return fmt.Errorf("response tenant_scope_ref mismatch: got %q want %q", entry.TenantScopeRef, tenantScopeRef)
	}
	return nil
}

func ledgerEntryFromResponse(resp gatewayhttp.AuditVerifyResponse) (auditledger.LedgerEntry, error) {
	prev, err := gatewayhttp.ParseAuditRootHex(resp.ChainProof.PrevMerkleRoot)
	if err != nil {
		return auditledger.LedgerEntry{}, fmt.Errorf("prev_merkle_root: %w", err)
	}
	root, err := gatewayhttp.ParseAuditRootHex(resp.ChainProof.MerkleRoot)
	if err != nil {
		return auditledger.LedgerEntry{}, fmt.Errorf("merkle_root: %w", err)
	}
	return auditledger.LedgerEntry{
		LedgerID:          resp.LedgerEntry.LedgerID,
		Timestamp:         resp.LedgerEntry.Timestamp,
		RequestID:         resp.LedgerEntry.RequestID,
		TenantID:          resp.LedgerEntry.TenantID,
		TenantScopeRef:    resp.LedgerEntry.TenantScopeRef,
		HopChain:          resp.LedgerEntry.HopChain,
		ModelChain:        resp.LedgerEntry.ModelChain,
		PrevMerkleRoot:    prev,
		MerkleRoot:        root,
		PubkeyFingerprint: resp.ChainProof.PubkeyFingerprint,
		Signature:         resp.ChainProof.Signature,
	}, nil
}

func fetchJSON(client *http.Client, target string, dst any) error {
	res, err := client.Get(target)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s returned HTTP %d: %s", target, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(res.Body).Decode(dst)
}

type pubkeyDoc struct {
	Fingerprint       string      `json:"fingerprint"`
	PubkeyFingerprint string      `json:"pubkey_fingerprint"`
	KID               string      `json:"kid"`
	Current           string      `json:"current"`
	PublicKey         string      `json:"public_key"`
	PublicKeyEd25519  string      `json:"public_key_ed25519"`
	Pubkey            string      `json:"pubkey"`
	X                 string      `json:"x"`
	Status            string      `json:"status"`
	Keys              []pubkeyDoc `json:"keys"`
	Revoked           []pubkeyDoc `json:"revoked"`
}

func fetchPubKey(client *http.Client, target, wantFP string) (ed25519.PublicKey, error) {
	res, err := client.Get(target)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pubkey URL returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	material, err := findPubKeyMaterial(body, wantFP)
	if err != nil {
		return nil, err
	}
	return decodePubKeyMaterial(material)
}

func findPubKeyMaterial(body []byte, wantFP string) (string, error) {
	var doc pubkeyDoc
	if err := json.Unmarshal(body, &doc); err == nil {
		if material, ok := pubkeyMaterialFromDoc(doc, wantFP); ok {
			return material, nil
		}
		var byFingerprint map[string]string
		if err := json.Unmarshal(body, &byFingerprint); err == nil && byFingerprint[wantFP] != "" {
			return byFingerprint[wantFP], nil
		}
		return "", fmt.Errorf("pubkey fingerprint %s not found", wantFP)
	}
	return strings.TrimSpace(string(body)), nil
}

func pubkeyMaterialFromDoc(doc pubkeyDoc, wantFP string) (string, bool) {
	fp := fingerprintFromDoc(doc)
	material := materialFromDoc(doc)
	if material != "" && (fp == "" || fp == wantFP) {
		return material, true
	}
	for _, key := range doc.Keys {
		if material, ok := pubkeyMaterialFromDoc(key, wantFP); ok {
			return material, true
		}
	}
	return "", false
}

func fingerprintFromDoc(doc pubkeyDoc) string {
	if strings.TrimSpace(doc.Fingerprint) != "" {
		return strings.TrimSpace(doc.Fingerprint)
	}
	if strings.TrimSpace(doc.PubkeyFingerprint) != "" {
		return strings.TrimSpace(doc.PubkeyFingerprint)
	}
	return strings.TrimSpace(doc.KID)
}

func materialFromDoc(doc pubkeyDoc) string {
	for _, material := range []string{doc.PublicKey, doc.PublicKeyEd25519, doc.Pubkey, doc.X} {
		if strings.TrimSpace(material) != "" {
			return strings.TrimSpace(material)
		}
	}
	return ""
}

func keyStatusFromDoc(doc pubkeyDoc) string {
	status := strings.TrimSpace(doc.Status)
	if status == "" {
		return "active"
	}
	return status
}

func decodeFlexibleBase64(value string) ([]byte, error) {
	cleaned := strings.TrimSpace(value)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw, err := enc.DecodeString(cleaned)
		if err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("invalid base64 material")
}

func decodePubKeyMaterial(material string) (ed25519.PublicKey, error) {
	cleaned := strings.TrimSpace(strings.TrimPrefix(material, "ed25519:"))
	if raw, err := hex.DecodeString(cleaned); err == nil && len(raw) == ed25519.PublicKeySize {
		return ed25519.PublicKey(raw), nil
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		raw, err := enc.DecodeString(cleaned)
		if err == nil && len(raw) == ed25519.PublicKeySize {
			return ed25519.PublicKey(raw), nil
		}
	}
	return nil, fmt.Errorf("invalid ed25519 public key material")
}
