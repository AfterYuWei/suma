package notification

import "time"

// Expect marks an explicit user-requested lifecycle change before Docker sees
// it. This bounded runtime-only marker suppresses expected stop/restart events.
func (s *Service) Expect(nodeID, containerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expected == nil {
		s.expected = map[string]time.Time{}
	}
	now := s.deps.Now()
	for key, expiry := range s.expected {
		if !expiry.After(now) {
			delete(s.expected, key)
		}
	}
	if len(s.expected) >= 10000 {
		clear(s.expected)
	}
	s.expected[nodeID+"|"+containerID] = now.Add(2 * time.Minute)
}
func (s *Service) IsExpected(nodeID, containerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expected[nodeID+"|"+containerID].After(s.deps.Now())
}
