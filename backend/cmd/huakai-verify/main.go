package main

import (
	"crypto/ed25519"
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
	gatewayURL := fs.String("gateway-url", "", "HUAKAI gateway base URL")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(out, "❌ audit verification failed: %v\n", err)
		return 2
	}
	if *pubkeyURL == "" || *requestID == "" || *gatewayURL == "" {
		fmt.Fprintln(out, "❌ audit verification failed: --pubkey-url, --request-id, and --gateway-url are required")
		return 2
	}
	if client == nil {
		client = http.DefaultClient
	}

	verify, tree, err := fetchAudit(client, *gatewayURL, *requestID)
	if err != nil {
		fmt.Fprintf(out, "❌ audit verification failed: %v\n", err)
		return 1
	}
	pub, err := fetchPubKey(client, *pubkeyURL, verify.ChainProof.PubkeyFingerprint)
	if err != nil {
		fmt.Fprintf(out, "❌ audit verification failed: %v\n", err)
		return 1
	}
	entry, err := verifyEntryProof(verify, pub)
	if err != nil {
		fmt.Fprintf(out, "❌ audit verification failed: %v\n", err)
		return 1
	}

	fmt.Fprintln(out, "✅ audit verification passed")
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

func fetchAudit(client *http.Client, gatewayURL, requestID string) (gatewayhttp.AuditVerifyResponse, gatewayhttp.AuditMerkleTreeResponse, error) {
	var verify gatewayhttp.AuditVerifyResponse
	verifyURL := strings.TrimRight(gatewayURL, "/") + "/v1/audit/verify?request_id=" + url.QueryEscape(requestID)
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
	PublicKey         string      `json:"public_key"`
	PublicKeyEd25519  string      `json:"public_key_ed25519"`
	Pubkey            string      `json:"pubkey"`
	Keys              []pubkeyDoc `json:"keys"`
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
	fp := doc.Fingerprint
	if fp == "" {
		fp = doc.PubkeyFingerprint
	}
	material := doc.PublicKey
	if material == "" {
		material = doc.PublicKeyEd25519
	}
	if material == "" {
		material = doc.Pubkey
	}
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
