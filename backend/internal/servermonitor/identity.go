package servermonitor

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID        uuid.UUID
	StartedAt time.Time
}

func ResolveIdentity(cfg Config) (Identity, error) {
	if configured := strings.ToLower(strings.TrimSpace(cfg.NodeID)); configured != "" {
		identity := Identity{
			NodeID:      configured,
			DisplayName: strings.TrimSpace(cfg.DisplayName),
			Source:      IdentitySourceConfigured,
			Stable:      true,
		}
		if identity.DisplayName == "" {
			identity.DisplayName = configured
		}
		if !nodeIDPattern.MatchString(identity.NodeID) {
			return Identity{}, errors.New("HUAKAI_SERVER_MONITOR_NODE_ID must be a 3-64 character lowercase opaque slug")
		}
		if len(identity.DisplayName) > 128 {
			return Identity{}, errors.New("HUAKAI_SERVER_MONITOR_DISPLAY_NAME exceeds 128 characters")
		}
		return identity, nil
	}

	runtimeIdentity, err := os.Hostname()
	if err != nil || strings.TrimSpace(runtimeIdentity) == "" {
		return Identity{}, errors.New("server monitor cannot derive a runtime identity; configure HUAKAI_SERVER_MONITOR_NODE_ID")
	}
	digest := sha256.Sum256([]byte("huakai-server-monitor-v1\x00" + runtimeIdentity))
	nodeID := "builtin-" + hex.EncodeToString(digest[:8])
	displayName := strings.TrimSpace(cfg.DisplayName)
	if displayName == "" {
		displayName = "gateway-" + hex.EncodeToString(digest[:4])
	}
	return Identity{
		NodeID:      nodeID,
		DisplayName: displayName,
		Source:      IdentitySourceRuntimeIdentityHash,
		Stable:      false,
	}, nil
}

func NewSession(now time.Time) (Session, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, StartedAt: now.UTC()}, nil
}

func NodeLeaseKey(nodeID string) int64 {
	digest := sha256.Sum256([]byte("huakai-server-monitor-node-lease-v1\x00" + nodeID))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func CleanupLeaseKey() int64 {
	digest := sha256.Sum256([]byte("huakai-server-monitor-cleanup-lease-v1"))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
