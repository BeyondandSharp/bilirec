package webhook

// AccountAuthFailed emits the global account authentication failure event.
// The existing webhook envelope (EventType/EventTimestamp/EventId/EventData) is
// reused; EventData identifies the failure stage and carries the error text.
func (s *Service) AccountAuthFailed(stage, message string, err error) {
	if !s.enabled() {
		return
	}
	data := accountAuthEventData{
		Stage:   stage,
		Message: message,
	}
	if err != nil {
		data.Error = err.Error()
	}
	s.emitAny(EventAccountAuthFailed, 0, data)
}
