package fsjournal

// QueryPlanForFind exposes the read-only query plan for proving the lookup index is selected.
func (s *Store) QueryPlanForFind() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT resource,version,run_id,seq FROM events WHERE resource=? AND version=? ORDER BY run_id,seq`, "r", uint64(1))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return "", err
		}
		out += detail + "\n"
	}
	return out, rows.Err()
}
