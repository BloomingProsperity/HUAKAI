package bindingfallback

import "encoding/json"

// Transition 是一次已获准的绑定类别转移。它只记录稳定枚举，既可供
// executor 激活目标 phase，也可安全写入路由审计。
type Transition struct {
	From    Class
	To      Class
	Trigger Signal
}

type evidenceItem struct {
	signal         Signal
	retryPermitted bool
}

// Evidence 聚合同一 normal phase 的失败证据。不同目标类别混合时会
// fail-closed，避免用最后一次失败覆盖前序事实。
type Evidence struct {
	target Class
	items  []evidenceItem
	mixed  bool
}

// Add 记录一次尚未交付的失败，并返回其目标类别。
func (e *Evidence) Add(signal Signal, retryPermitted bool) (Class, bool) {
	if e == nil || IsTerminal(signal) {
		return "", false
	}
	target, ok := TargetClass(signal)
	if !ok {
		return "", false
	}
	if e.target == "" {
		e.target = target
	} else if e.target != target {
		e.mixed = true
	}
	e.items = append(e.items, evidenceItem{signal: signal, retryPermitted: retryPermitted})
	return target, true
}

// Transition 在 normal phase 确实耗尽后，对全部证据逐项执行统一终态门。
func (e Evidence) Transition(state TransitionState) (Transition, bool) {
	if e.target == "" || e.mixed || len(e.items) == 0 {
		return Transition{}, false
	}
	for _, item := range e.items {
		itemState := state
		itemState.RetryPermitted = item.retryPermitted
		target, allowed := AllowTransition(item.signal, itemState)
		if !allowed || target != e.target {
			return Transition{}, false
		}
	}
	return Transition{From: ClassNormal, To: e.target, Trigger: e.auditTrigger()}, true
}

func (e Evidence) auditTrigger() Signal {
	first := e.items[0].signal
	for _, item := range e.items[1:] {
		if item.signal == first {
			continue
		}
		switch e.target {
		case ClassQuota:
			return SignalPoolCapacityExhausted
		case ClassContextWindow:
			return SignalLocalContextWindow
		case ClassSafety:
			return SignalUpstreamContentPolicy
		case ClassManual:
			return SignalTransientConnectionFailure
		}
	}
	return first
}

// FailureObservation 是一次 normal attempt 失败后推进状态机所需的最小输入。
type FailureObservation struct {
	Signal            Signal
	RetryPermitted    bool
	MorePrimary       bool
	TargetConfigured  bool
	LocalSafetyPassed bool
	DeliveryStarted   bool
}

// NextAction 是 executor 在失败后唯一允许采取的动作。
type NextAction uint8

const (
	ActionStop NextAction = iota
	ActionContinuePrimary
	ActionTransition
)

// StepDecision 是 Coordinator 对一次失败的纯决策结果。
type StepDecision struct {
	Action     NextAction
	Transition Transition
}

// Coordinator 保证一个模型请求最多发生一次 normal→目标类别转移。
type Coordinator struct {
	evidence       Evidence
	transitionUsed bool
}

// Observe 先聚合失败，再决定继续 normal、跨类或原地终止。
func (c *Coordinator) Observe(observation FailureObservation) StepDecision {
	if c == nil || observation.DeliveryStarted || IsTerminal(observation.Signal) {
		return StepDecision{Action: ActionStop}
	}
	_, degradable := c.evidence.Add(observation.Signal, observation.RetryPermitted)
	if observation.MorePrimary {
		if observation.RetryPermitted || (degradable && observation.TargetConfigured) {
			return StepDecision{Action: ActionContinuePrimary}
		}
		return StepDecision{Action: ActionStop}
	}
	transition, ok := c.evidence.Transition(TransitionState{
		CurrentClass:      ClassNormal,
		PrimaryExhausted:  true,
		TargetConfigured:  observation.TargetConfigured,
		LocalSafetyPassed: observation.LocalSafetyPassed,
		DeliveryStarted:   observation.DeliveryStarted,
		TransitionUsed:    c.transitionUsed,
	})
	if !ok {
		return StepDecision{Action: ActionStop}
	}
	c.transitionUsed = true
	return StepDecision{Action: ActionTransition, Transition: transition}
}

// AnnotateRoutingReason 把脱敏的类别转移写入 selector 路由原因。
func AnnotateRoutingReason(raw []byte, transition Transition) []byte {
	payload := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			payload = map[string]any{"routing_reason_error": "selector_reason_invalid"}
		}
	}
	if payload == nil {
		payload = map[string]any{"routing_reason_error": "selector_reason_empty"}
	}
	payload["class_transition"] = map[string]string{
		"from": string(transition.From), "to": string(transition.To), "trigger": string(transition.Trigger),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return encoded
}
