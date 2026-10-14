package services

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// PromotionPaths is deliberately bounded. A path is internal targeting data,
// never an API response or a substitute for current audience checks at delivery.
func PromotionPaths(ctx context.Context, tx pgx.Tx, actor string) ([][]string, error) {
	rows, err := tx.Query(ctx, `WITH RECURSIVE walk(path,depth) AS (
 SELECT ARRAY[$1::uuid],0 WHERE EXISTS(SELECT 1 FROM social_profiles WHERE user_id=$1::uuid AND connection_promotions)
 UNION ALL SELECT w.path||n.peer,w.depth+1 FROM walk w CROSS JOIN LATERAL (
 SELECT CASE WHEN f.user_low=w.path[array_length(w.path,1)] THEN f.user_high ELSE f.user_low END AS peer
 FROM social_connections f WHERE f.state='accepted' AND w.path[array_length(w.path,1)] IN(f.user_low,f.user_high)
 ORDER BY f.updated_at DESC,f.id LIMIT 20) n
 JOIN social_profiles p ON p.user_id=n.peer AND p.connection_promotions
 WHERE w.depth<3 AND NOT n.peer=ANY(w.path) AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE (b.blocker_id=n.peer AND b.blocked_id=ANY(w.path)) OR (b.blocked_id=n.peer AND b.blocker_id=ANY(w.path))))
 SELECT w.path::text[] FROM walk w JOIN provider_organizations o ON o.owner_user_id=w.path[array_length(w.path,1)] JOIN provider_public_identity i ON i.provider_id=o.id
 WHERE w.depth>0 AND i.verified AND i.paid_active ORDER BY w.depth,o.id LIMIT 200`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := [][]string{}
	for rows.Next() {
		var path []string
		if err = rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}
