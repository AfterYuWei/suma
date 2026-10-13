package containermonitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

func (s *Service) Instances(ctx context.Context, q InstanceQuery) (Instances, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	out := Instances{Items: []database.MonitoringInstance{}, Nodes: []HistoricalNode{}, Page: q.Page}
	if q.Limit < 1 || q.Limit > 100 || q.Page > 100000 || len(q.Search) > 256 {
		return out, ErrInput
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.Settings().RetentionDays)
	if err := s.db.WithContext(ctx).Raw("SELECT DISTINCT ON (node_id) node_id AS id,node_name AS name FROM monitoring_instances WHERE last_seen>=? ORDER BY node_id,last_seen DESC", cutoff).Scan(&out.Nodes).Error; err != nil {
		return out, err
	}
	query := s.db.WithContext(ctx).Model(&database.MonitoringInstance{}).Where("last_seen >= ?", cutoff)
	if q.NodeID != "" {
		query = query.Where("node_id = ?", q.NodeID)
	}
	if q.ContainerID != "" {
		query = query.Where("container_id = ?", q.ContainerID)
	}
	if q.Search != "" {
		query = query.Where("strpos(lower(name),lower(?))>0 OR strpos(container_id,?)>0 OR strpos(lower(node_name),lower(?))>0 OR strpos(lower(project),lower(?))>0", q.Search, q.Search, q.Search, q.Search)
	}
	if !q.Since.IsZero() {
		query = query.Where("last_seen >= ?", q.Since)
	}
	if !q.Until.IsZero() {
		query = query.Where("first_seen <= ?", q.Until)
	}
	if err := query.Count(&out.Total).Error; err != nil {
		return out, err
	}
	err := query.Order("last_seen DESC, id").Limit(q.Limit).Offset((q.Page - 1) * q.Limit).Find(&out.Items).Error
	return out, err
}
func (s *Service) Query(ctx context.Context, q Query) (Result, error) {
	out := Result{Series: []Series{}, RetainedSince: time.Now().UTC().AddDate(0, 0, -s.Settings().RetentionDays)}
	if len(q.Targets) < 1 || len(q.Targets) > 4 {
		return out, ErrInput
	}
	maxPoints := q.MaxPoints
	if maxPoints == 0 {
		maxPoints = 1440
	}
	if maxPoints < 2 || maxPoints > 1440 {
		return out, ErrInput
	}
	// All targets use the same bucket width, including different comparison ranges.
	step := 5
	for _, t := range q.Targets {
		if !identifier.MatchString(t.NodeID) || len(t.EngineID) > 128 || t.EngineID == "" || !fullID.MatchString(t.ContainerID) || t.Since.IsZero() || t.Until.Before(t.Since) || t.Until.Equal(t.Since) || t.Until.Sub(t.Since) > 30*24*time.Hour || t.Until.After(time.Now().Add(time.Minute)) {
			return out, ErrInput
		}
		required := int(math.Ceil(t.Until.Sub(t.Since).Seconds() / float64(maxPoints)))
		selected := max(5, (required+4)/5*5)
		for _, candidate := range []int{5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600} {
			if candidate >= required {
				selected = candidate
				break
			}
		}
		step = max(step, selected)
	}
	for _, target := range q.Targets {
		series, err := s.queryTarget(ctx, target, step, maxPoints, out.RetainedSince)
		if err != nil {
			return out, err
		}
		out.Series = append(out.Series, series)
	}
	return out, nil
}
func (s *Service) queryTarget(ctx context.Context, t Target, step, maxPoints int, cutoff time.Time) (Series, error) {
	out := Series{Instance: database.MonitoringInstance{ID: InstanceID(t.NodeID, t.EngineID, t.ContainerID), NodeID: t.NodeID, EngineID: t.EngineID, ContainerID: t.ContainerID}, Target: t, Points: []Point{}, Summary: map[string]Aggregate{}, Gaps: []database.MonitoringGap{}, Coverage: "not_collected", StepSeconds: step}
	err := s.db.WithContext(ctx).First(&out.Instance, "id = ?", out.Instance.ID).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return out, err
	}
	since := t.Since
	if since.Before(cutoff) {
		since = cutoff
		out.Coverage = "expired"
	}
	if !since.Before(t.Until) {
		return out, nil
	}
	type row struct {
		Time        time.Time
		StartedAt   string
		Segment     string
		MetricsJSON string
	}
	expressions := []string{}
	for _, field := range Fields {
		expressions = append(expressions, fmt.Sprintf("'%s',json_build_object('avg',avg(%s),'min',min(%s),'max',max(%s),'count',count(%s))", field, field, field, field, field))
	}
	aggregates := "json_build_object(" + strings.Join(expressions, ",") + ")::text"
	sql := "WITH base AS (SELECT *, lag(recorded_at) OVER (ORDER BY recorded_at) AS prev_time, lag(started_at) OVER (ORDER BY recorded_at) AS prev_start, lag(session) OVER (ORDER BY recorded_at) AS prev_session FROM monitoring_samples WHERE instance_id=? AND recorded_at>=? AND recorded_at<=?), segmented AS (SELECT *, sum(CASE WHEN prev_time IS NULL OR started_at<>prev_start OR session<>prev_session OR extract(epoch FROM recorded_at-prev_time)>interval_seconds*2.5 THEN 1 ELSE 0 END) OVER (ORDER BY recorded_at) AS segment_number FROM base) SELECT date_bin(make_interval(secs=>?),recorded_at,?::timestamptz) AS time,started_at,session||':'||segment_number AS segment," + aggregates + " AS metrics_json FROM segmented GROUP BY time,started_at,session,segment_number ORDER BY time DESC LIMIT ?"
	rows := []row{}
	if err := s.db.WithContext(ctx).Raw(sql, out.Instance.ID, since, t.Until, step, t.Since, maxPoints+1).Scan(&rows).Error; err != nil {
		return out, err
	}
	if len(rows) > maxPoints {
		rows = rows[:maxPoints]
		out.Truncated = true
		out.Coverage = "truncated"
	}
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		values := map[string]Aggregate{}
		if err := json.Unmarshal([]byte(r.MetricsJSON), &values); err != nil {
			return out, err
		}
		out.Points = append(out.Points, Point{Time: r.Time, StartedAt: r.StartedAt, Segment: r.Segment, Values: values})
	}
	var summary string
	if err := s.db.WithContext(ctx).Raw("SELECT "+aggregates+" FROM monitoring_samples WHERE instance_id=? AND recorded_at>=? AND recorded_at<=?", out.Instance.ID, since, t.Until).Scan(&summary).Error; err != nil {
		return out, err
	}
	if summary != "" {
		if err := json.Unmarshal([]byte(summary), &out.Summary); err != nil {
			return out, err
		}
	}
	if err := s.db.WithContext(ctx).Where("node_id=? AND engine_id=? AND (instance_id='' OR instance_id=?) AND until>=? AND since<=?", t.NodeID, t.EngineID, out.Instance.ID, since, t.Until).Order("since").Limit(1000).Find(&out.Gaps).Error; err != nil {
		return out, err
	}
	if len(out.Points) > 0 && out.Coverage == "not_collected" {
		out.Coverage = "recorded"
	}
	if len(out.Gaps) > 0 && out.Coverage == "recorded" {
		out.Coverage = "partial"
	}
	if len(out.Points) == 0 && excluded(s.Settings(), Node{ID: t.NodeID, Enabled: true}, Inventory{ID: t.ContainerID, Project: out.Instance.Project, Service: out.Instance.Service}) {
		out.Coverage = "excluded"
	}
	return out, nil
}
func (s *Service) Events(ctx context.Context, id string, since, until time.Time, kind, cursor string, limit int) (EventPage, error) {
	out := EventPage{Items: []database.MonitoringEvent{}}
	if !fullID.MatchString(id) || since.IsZero() || !until.After(since) || until.Sub(since) > 30*24*time.Hour || limit < 1 || limit > 256 {
		return out, ErrInput
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -s.Settings().RetentionDays)
	if since.Before(cutoff) {
		since = cutoff
	}
	query := s.db.WithContext(ctx).Where("instance_id=? AND time>=? AND time<=?", id, since, until)
	switch kind {
	case "", "all":
	case "health":
		query = query.Where("action = 'health_status'")
	case "abnormal":
		query = query.Where("action='oom' OR health_status='unhealthy' OR exit_code<>0")
	case "lifecycle":
		query = query.Where("action NOT IN ('oom','health_status')")
	default:
		return out, ErrInput
	}
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) > 160 {
			return out, ErrInput
		}
		parts := strings.SplitN(string(decoded), "|", 2)
		if len(parts) != 2 || !fullID.MatchString(parts[1]) {
			return out, ErrInput
		}
		at, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return out, ErrInput
		}
		query = query.Where("(time,id) > (?,?)", at, parts[1])
	}
	if err := query.Order("time,id").Limit(limit + 1).Find(&out.Items).Error; err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[len(out.Items)-1]
		out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.Time.UTC().Format(time.RFC3339Nano) + "|" + last.ID))
	}
	return out, nil
}
