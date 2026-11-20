package store

// Reports limited to the users of one admin (a reseller's dashboard).

// OwnerUsage returns hourly totals since `since` for the users of one admin.
func (s *Store) OwnerUsage(owner, since int64) ([]UsagePoint, error) {
	rows, err := s.DB.Query(`SELECT g.hour, SUM(g.up), SUM(g.down) FROM usage g JOIN users u ON u.id=g.user_id
		WHERE g.hour>=? AND u.owner_id=? GROUP BY g.hour ORDER BY g.hour`, since/3600, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsagePoint{}
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.Hour, &p.Up, &p.Down); err != nil {
			return nil, err
		}
		p.Hour *= 3600
		out = append(out, p)
	}
	return out, rows.Err()
}

// OwnerTotal returns the traffic of one admin's users since `since`.
func (s *Store) OwnerTotal(owner, since int64) (up, down int64) {
	s.DB.QueryRow(`SELECT COALESCE(SUM(g.up),0), COALESCE(SUM(g.down),0) FROM usage g JOIN users u ON u.id=g.user_id
		WHERE g.hour>=? AND u.owner_id=?`, since/3600, owner).Scan(&up, &down)
	return
}

// OwnerTop ranks one admin's users by traffic since `since`.
func (s *Store) OwnerTop(owner, since int64, limit int) ([]TopUser, error) {
	rows, err := s.DB.Query(`SELECT u.id, u.name, SUM(g.up+g.down) AS b FROM usage g JOIN users u ON u.id=g.user_id
		WHERE g.hour>=? AND u.owner_id=? GROUP BY u.id ORDER BY b DESC LIMIT ?`, since/3600, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopUser{}
	for rows.Next() {
		var t TopUser
		if err := rows.Scan(&t.ID, &t.Name, &t.Bytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
