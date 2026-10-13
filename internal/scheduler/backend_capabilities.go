package scheduler

import "robot/internal/shared"

// requireBackendCapability keeps unsupported operations from entering the
// shared actor workflow. Legacy tests may leave backendInfo empty and retain
// the original all-capabilities behavior.
func (m *RobotManager) requireBackendCapability(capability shared.BackendCapability) error {
	if m == nil || m.backendInfo.ID == "" {
		return nil
	}
	return m.backendInfo.Require(capability)
}

func (m *RobotManager) supportsBackendCapability(capability shared.BackendCapability) bool {
	return m == nil || m.backendInfo.ID == "" || m.backendInfo.Supports(capability)
}

func (m *RobotManager) RequireCapability(capability shared.BackendCapability) error {
	return m.requireBackendCapability(capability)
}

// requireBroadcastShout is the gate for every shout that names no target: the
// automatic template turn and the manual shout commands. A backend that only
// declares directed chat still supports CapabilityShout, so the capability
// check alone would let one through and it would fail later, after the actor
// turn had already been booked as a failure. Refusing here names the reason
// once, at the point where the channel is still a choice.
func (m *RobotManager) requireBroadcastShout() error {
	if err := m.requireBackendCapability(shared.CapabilityShout); err != nil {
		return err
	}
	if m == nil || m.backendInfo.ID == "" {
		return nil
	}
	if m.backendInfo.Capabilities[shared.CapabilityShout].Mode != shared.ShoutModeDirected {
		return nil
	}
	reason := m.backendInfo.Capabilities[shared.CapabilityShout].Reason
	if reason == "" {
		reason = "this backend delivers chat to a named character only"
	}
	return shared.UnsupportedCapabilityError{
		Backend:   m.backendInfo.ID,
		Operation: shared.CapabilityShout,
		Reason:    "no broadcast channel is available for a target-less shout: " + reason,
	}
}
