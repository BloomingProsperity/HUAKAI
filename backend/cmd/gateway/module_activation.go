package main

import (
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

type activationEndpointState struct {
	name     string
	injected bool
	active   bool
}

func activationBoolPtr(v bool) *bool { return &v }

func activationIntPtr(v int) *int { return &v }

func endpointActivation(name string, dependencyAvailable bool) activationEndpointState {
	return activationEndpointState{
		name:     name,
		injected: dependencyAvailable,
		active:   dependencyAvailable,
	}
}

func activationSnapshot(constructed bool, backend string, sharedSafe bool, endpoints ...activationEndpointState) *moduleregistry.ActivationSnapshot {
	declared := true
	injected := false
	active := false
	projected := make([]moduleregistry.ActivationEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		if ep.injected {
			injected = true
		}
		if ep.active {
			active = true
		}
		projected = append(projected, moduleregistry.ActivationEndpoint{
			Name:     ep.name,
			Injected: activationBoolPtr(ep.injected),
			Active:   activationBoolPtr(ep.active),
		})
	}
	return &moduleregistry.ActivationSnapshot{
		Declared:    &declared,
		Constructed: activationBoolPtr(constructed),
		Injected:    activationBoolPtr(injected),
		Active:      activationBoolPtr(active),
		SharedSafe:  activationBoolPtr(sharedSafe),
		Observable:  activationBoolPtr(true),
		Backend:     backend,
		Endpoints:   projected,
	}
}

func selectorActivation(d *deps) *moduleregistry.ActivationSnapshot {
	available := d.selector != nil
	endpoints := []activationEndpointState{
		endpointActivation("chat", available),
		endpointActivation("completions", available),
		endpointActivation("embeddings", available),
		endpointActivation("rerank", available),
		endpointActivation("images", available),
		endpointActivation("audio", available),
	}
	snapshot := activationSnapshot(available, "mixed", false, endpoints...)
	if d.selectorConfig != nil {
		snapshot.Mode = string(d.selectorConfig.Mode)
		switch d.selectorConfig.Mode {
		case runtimeconfig.PoolSelectorModeCanary:
			snapshot.TrafficPercent = activationIntPtr(d.selectorConfig.CanaryPercent)
		case runtimeconfig.PoolSelectorModeShadow:
			snapshot.TrafficPercent = activationIntPtr(d.selectorConfig.ShadowPercent)
		}
	}
	return snapshot
}

func queueWaitActivation(d *deps) *moduleregistry.ActivationSnapshot {
	injected := d.queueWaiter != nil
	snapshot := activationSnapshot(true, "local", false,
		activationEndpointState{name: "chat", injected: injected, active: true},
		endpointActivation("completions", false),
		endpointActivation("embeddings", false),
		endpointActivation("rerank", false),
		endpointActivation("images", false),
		endpointActivation("audio", false),
	)
	if injected {
		snapshot.Mode = "composition-root"
	} else {
		snapshot.Mode = "handler-default"
	}
	return snapshot
}

func settlementRecoveryActivation(d *deps) *moduleregistry.ActivationSnapshot {
	constructed := d.dlqService != nil
	active := constructed && d.settleRecoveryReady
	return activationSnapshot(constructed, "postgresql", true,
		activationEndpointState{name: "chat", injected: constructed, active: active},
		activationEndpointState{name: "completions", injected: constructed, active: active},
		endpointActivation("embeddings", false),
		endpointActivation("rerank", false),
		activationEndpointState{name: "images", injected: constructed, active: active},
		endpointActivation("audio", false),
	)
}

func channelHealthActivation(d *deps) *moduleregistry.ActivationSnapshot {
	constructed := d.channelHealth != nil
	selectorWired := constructed && d.selector != nil
	backend := "postgresql"
	sharedSafe := true
	if d.authCooldown != nil {
		backend = "mixed"
		sharedSafe = false
	}
	return activationSnapshot(constructed, backend, sharedSafe,
		endpointActivation("chat", constructed),
		endpointActivation("completions", selectorWired),
		endpointActivation("embeddings", selectorWired),
		endpointActivation("rerank", selectorWired),
		endpointActivation("images", selectorWired),
		endpointActivation("audio", selectorWired),
	)
}

func responseCacheActivation(d *deps) *moduleregistry.ActivationSnapshot {
	available := d.responseCache != nil
	snapshot := activationSnapshot(available, "local", false,
		endpointActivation("chat", available),
		endpointActivation("completions", false),
		endpointActivation("embeddings", false),
		endpointActivation("rerank", false),
		endpointActivation("images", false),
		endpointActivation("audio", false),
	)
	snapshot.Mode = "non-stream"
	return snapshot
}

func modelRegistryActivation(d *deps) *moduleregistry.ActivationSnapshot {
	available := d.modelRegistry != nil
	return activationSnapshot(available, "postgresql", true,
		endpointActivation("chat", available),
		endpointActivation("completions", available),
		endpointActivation("embeddings", available),
		endpointActivation("rerank", available),
		endpointActivation("images", available),
		endpointActivation("audio", available),
	)
}
