// Package storage 提供基于 SQL 的 core.Storage 接口实现。
// 本包支持 SQLite、MySQL 和 PostgreSQL 三种数据库后端，
// 通过统一的 sqlStorage 结构体共享通用的数据库操作逻辑，
// 各数据库驱动仅负责连接建立和方言差异（如占位符格式）。
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// sqlStorage 是 SQLite、MySQL 和 PostgreSQL 共用的 SQL 存储实现。
// 它封装了数据库连接池（*sql.DB）和方言相关的占位符生成函数。
type sqlStorage struct {
	// db 是底层数据库连接池，所有 SQL 操作都通过它执行。
	db *sql.DB
	// placeholder 是方言相关的占位符生成函数。
	// SQLite 和 MySQL 使用 "?"（忽略参数 n），
	// PostgreSQL 使用 "$1", "$2", ... （依赖参数 n 表示第几个参数）。
	placeholder func(n int) string
}

// ---------------------------------------------------------------------------
// Schema 创建（通过回调实现方言差异化）
// ---------------------------------------------------------------------------

// createSchema 创建所有必要的数据库表和索引。
// autoIncrement 参数是方言相关的自增主键语法片段：
//   - SQLite:     "INTEGER PRIMARY KEY AUTOINCREMENT"
//   - MySQL:      "INT AUTO_INCREMENT PRIMARY KEY"
//   - PostgreSQL: "SERIAL PRIMARY KEY"
//
// 该函数会创建以下三张表：
//   - calls：记录每次 AI 模型调用的详细信息（请求、响应、延迟、费用等）
//   - audits：记录内容审核/审计日志（命中规则、跨度、动作等）
//   - privacy_mappings：记录隐私数据的脱敏映射关系（原始值 <-> 假值）
//
// 此外还会创建常用查询字段的索引以加速检索。
func (s *sqlStorage) createSchema(autoIncrement string) error {
	// calls 表：存储每次 AI 模型 API 调用的完整记录。
	// 字段说明：
	//   id             - 调用记录的唯一标识
	//   provider       - AI 服务提供商名称（如 openai、anthropic）
	//   model          - 使用的模型名称（如 gpt-4、claude-3）
	//   request_json   - 请求体的 JSON 序列化
	//   response_json  - 响应体的 JSON 序列化
	//   error          - 如果调用失败，记录错误信息
	//   latency_ms     - 调用延迟（毫秒）
	//   timestamp      - 调用的 Unix 时间戳
	//   tags_json      - 标签的 JSON 数组，用于分类和筛选
	//   fallback_from  - 如果是降级调用，记录原始 provider
	//   cost           - 本次调用的费用
	//   currency       - 费用货币单位
	callsSQL := `
		CREATE TABLE IF NOT EXISTS calls (
			id TEXT PRIMARY KEY,
			provider TEXT,
			model TEXT,
			request_json TEXT,
			response_json TEXT,
			error TEXT,
			latency_ms INTEGER,
			timestamp INTEGER,
			tags_json TEXT,
			fallback_from TEXT,
			cost REAL,
			currency TEXT
		);`

	// audits 表：存储内容审计/审核日志。
	// 主键使用方言相关的自增语法（通过 autoIncrement 参数注入）。
	auditsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS audits (
			id %s,
			timestamp INTEGER,
			stage TEXT,
			type TEXT,
			hits_json TEXT,
			spans_json TEXT,
			action TEXT,
			provider TEXT,
			model TEXT,
			content_hash TEXT,
			content_preview TEXT
		);`, autoIncrement)

	// privacy_mappings 表：存储隐私数据的脱敏映射。
	// 每条记录将一个"原始值"映射为一个"假值"，在同一 session 内保持一致性。
	mappingsSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS privacy_mappings (
			id %s,
			session_id TEXT NOT NULL,
			original TEXT NOT NULL,
			fake TEXT NOT NULL,
			type TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`, autoIncrement)

	// 依次执行三张表的 CREATE TABLE 语句。
	for _, stmt := range []string{callsSQL, auditsSQL, mappingsSQL} {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}

	// 创建索引以加速常用查询：
	// - idx_calls_provider：按 provider 查询调用记录
	// - idx_calls_timestamp：按时间范围查询调用记录
	// - idx_audits_timestamp：按时间范围查询审计日志
	// - idx_mappings_session：按 session_id 查询映射关系
	// - idx_mappings_session_original：按 session_id + original 联合查询（用于去重/查找）
	// - idx_mappings_session_fake：按 session_id + fake 联合查询（用于反向查找）
	idx := []string{
		`CREATE INDEX IF NOT EXISTS idx_calls_provider ON calls(provider);`,
		`CREATE INDEX IF NOT EXISTS idx_calls_timestamp ON calls(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_audits_timestamp ON audits(timestamp);`,
		`CREATE INDEX IF NOT EXISTS idx_mappings_session ON privacy_mappings(session_id);`,
		`CREATE INDEX IF NOT EXISTS idx_mappings_session_original ON privacy_mappings(session_id, original);`,
		`CREATE INDEX IF NOT EXISTS idx_mappings_session_fake ON privacy_mappings(session_id, fake);`,
	}
	for _, stmt := range idx {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 调用记录（Call History）相关操作
// ---------------------------------------------------------------------------

// RecordCall 将一次 AI 模型调用记录持久化到数据库中。
// 它会将请求体、响应体和标签列表序列化为 JSON 字符串后存入对应的 TEXT 字段。
// 使用 ExecContext 执行 INSERT 语句，支持通过 ctx 进行超时控制和取消。
func (s *sqlStorage) RecordCall(ctx context.Context, r core.CallRecord) error {
	// 将复杂对象序列化为 JSON 字符串以便存储到 TEXT 字段中。
	reqJSON, _ := json.Marshal(r.Request)
	respJSON, _ := json.Marshal(r.Response)
	tagsJSON, _ := json.Marshal(r.Tags)

	// 执行 INSERT 语句，共 12 个字段对应 12 个占位符。
	// r.Timestamp.Unix() 将时间转换为 Unix 时间戳存储，避免时区问题。
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO calls (id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency) VALUES ("+
			s.placeholders(12)+
			")",
		r.ID, r.Provider, r.Model, string(reqJSON), string(respJSON), r.Error, r.LatencyMs, r.Timestamp.Unix(), string(tagsJSON), r.FallbackFrom, r.Cost, r.Currency,
	)
	return err
}

// GetCalls 获取最近的调用记录列表，按时间戳降序排列。
// limit 参数控制返回的最大记录数，若 <= 0 则默认返回 100 条。
// 返回的切片会按时间从新到旧排序，方便前端直接展示。
func (s *sqlStorage) GetCalls(ctx context.Context, limit int) ([]core.CallRecord, error) {
	// 参数校验：限制默认值为 100，防止不传参时返回过多数据。
	if limit <= 0 {
		limit = 100
	}
	// 查询 calls 表全部字段，按 timestamp 降序排列，通过 LIMIT 限制返回条数。
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency FROM calls ORDER BY timestamp DESC LIMIT "+s.ph(1),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 确保无论成功与否都关闭 rows，释放游标资源。
	return s.scanCalls(rows, limit)
}

// GetCallsByTag 根据标签搜索调用记录。
// 它会在 tags_json 字段中通过 LIKE 模糊匹配查找包含指定 tag 的记录。
// tag 参数中的 SQL 通配符（% 和 _）会被转义，防止用户输入干扰 LIKE 语义。
// limit 参数控制返回的最大记录数，若 <= 0 则默认返回 100 条。
func (s *sqlStorage) GetCallsByTag(ctx context.Context, tag string, limit int) ([]core.CallRecord, error) {
	// 参数校验：限制默认值为 100。
	if limit <= 0 {
		limit = 100
	}
	// Bug S-03 修复：转义 tag 中的 SQL LIKE 通配符。
	// 用户输入的 tag 可能包含 "%" 或 "_" 字符，如果不转义，
	// "%" 会匹配任意字符串，"_" 会匹配任意单个字符，导致搜索结果不精确。
	// 例如 tag="100%" 不转义的话会变成匹配所有以 "100" 开头的字符串。
	escapedTag := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(tag)

	// 构建 LIKE 模式：前后加 % 表示只要 tags_json 中任意位置包含该 tag 即可匹配。
	// tags_json 是 JSON 数组格式（如 ["tag1","tag2"]），所以用 LIKE 做子串匹配。
	pattern := "%" + escapedTag + "%"
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency FROM calls WHERE tags_json LIKE "+s.ph(1)+" ORDER BY timestamp DESC LIMIT "+s.ph(2),
		pattern, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 确保无论成功与否都关闭 rows，释放游标资源。
	return s.scanCalls(rows, limit)
}

// GetCallsByProvider 根据服务提供商名称获取调用记录列表。
// 使用精确匹配（provider = ?）而非 LIKE，因为 provider 名称是确定性的标识。
// limit 参数控制返回的最大记录数，若 <= 0 则默认返回 100 条。
func (s *sqlStorage) GetCallsByProvider(ctx context.Context, provider string, limit int) ([]core.CallRecord, error) {
	// 参数校验：限制默认值为 100。
	if limit <= 0 {
		limit = 100
	}
	// 按 provider 精确过滤，按 timestamp 降序排列，通过 LIMIT 限制返回条数。
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, provider, model, request_json, response_json, error, latency_ms, timestamp, tags_json, fallback_from, cost, currency FROM calls WHERE provider = "+s.ph(1)+" ORDER BY timestamp DESC LIMIT "+s.ph(2),
		provider, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 确保无论成功与否都关闭 rows，释放游标资源。
	return s.scanCalls(rows, limit)
}

// GetProviderStats 获取各服务提供商的统计信息。
// 它通过一条聚合 SQL 按 provider 和 currency 分组，计算以下指标：
//   - COUNT(*)：总调用次数
//   - SUM(cost)：总费用（可能为 NULL，当没有记录时）
//   - AVG(latency_ms)：平均延迟（可能为 NULL，当没有记录时）
//   - SUM(CASE WHEN error != '' THEN 1 ELSE 0 END)：错误调用次数
//
// 返回一个 map，key 为 provider 名称，value 为对应的统计结构体。
func (s *sqlStorage) GetProviderStats(ctx context.Context) (map[string]core.ProviderStats, error) {
	// 聚合查询：按 provider 和 currency 分组，计算调用次数、总费用、平均延迟和错误次数。
	// 注意：当 calls 表为空时，SUM(cost) 和 AVG(latency_ms) 都会返回 NULL。
	rows, err := s.db.QueryContext(ctx,
		"SELECT provider, COUNT(*), SUM(cost), currency, AVG(latency_ms), SUM(CASE WHEN error != '' THEN 1 ELSE 0 END) FROM calls GROUP BY provider, currency",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 确保无论成功与否都关闭 rows，释放游标资源。

	stats := make(map[string]core.ProviderStats)
	for rows.Next() {
		var p core.ProviderStats
		var currency sql.NullString

		// Bug S-01 修复：使用 sql.NullFloat64 来接收 SUM(cost) 和 AVG(latency_ms)。
		// 当 calls 表为空或某个 provider 的所有 cost 值均为 NULL 时，
		// SUM(cost) 会返回 NULL 而不是 0。如果直接用 float64 接收，
		// database/sql 的 Scan 会 panic 或返回错误。
		// sql.NullFloat64 能安全地处理 NULL 值，通过 Valid 字段标识是否为 NULL。
		var totalCost sql.NullFloat64
		var avgLatency sql.NullFloat64

		// 注意：Scan 的字段顺序必须与 SELECT 列的顺序严格一致。
		// SELECT 顺序：provider, COUNT(*), SUM(cost), currency, AVG(latency_ms), SUM(error count)
		if err := rows.Scan(&p.Provider, &p.TotalCalls, &totalCost, &currency, &avgLatency, &p.ErrorCount); err != nil {
			return nil, err
		}

		// 将 sql.NullFloat64 转换为普通值。
		// 如果 SUM(cost) 为 NULL（Valid == false），则 TotalCost 默认为 0。
		p.TotalCost = totalCost.Float64
		p.Currency = currency.String
		// 将平均延迟从 float64 转换为 int64 毫秒。
		// 如果 AVG(latency_ms) 为 NULL（Valid == false），则 AvgLatency 默认为 0。
		p.AvgLatency = int64(avgLatency.Float64)

		stats[p.Provider] = p
	}
	// rows.Err() 检查迭代过程中是否有隐藏的错误（如网络中断）。
	return stats, rows.Err()
}

// scanCalls 将 sql.Rows 游标中的数据逐行扫描并转换为 CallRecord 切片。
// 它是 GetCalls、GetCallsByTag、GetCallsByProvider 共用的内部辅助函数。
// limit 参数用于预分配结果切片的容量，减少 append 时的内存分配次数。
//
// Bug S-07 修复：通过 make([]core.CallRecord, 0, limit) 预分配切片容量。
// 之前使用 var result []core.CallRecord（零值切片），每次 append 都可能触发
// 底层数组的扩容和数据拷贝。预分配可以显著减少内存分配开销，
// 特别是在 limit 较大（如 100）的场景下效果明显。
func (s *sqlStorage) scanCalls(rows *sql.Rows, limit int) ([]core.CallRecord, error) {
	// 预分配切片容量为 limit，避免 append 过程中频繁扩容。
	result := make([]core.CallRecord, 0, limit)
	for rows.Next() {
		var r core.CallRecord
		// reqJSON、respJSON、tagsJSON 是 JSON 格式的 TEXT 字段，
		// 先扫描为 string，再反序列化为对应的 Go 对象。
		var reqJSON, respJSON, tagsJSON string
		// tsUnix 用于接收 Unix 时间戳，之后转换为 time.Time。
		var tsUnix int64
		if err := rows.Scan(&r.ID, &r.Provider, &r.Model, &reqJSON, &respJSON, &r.Error, &r.LatencyMs, &tsUnix, &tagsJSON, &r.FallbackFrom, &r.Cost, &r.Currency); err != nil {
			return nil, err
		}
		// 将 Unix 时间戳转换为 time.Time 对象。
		r.Timestamp = time.Unix(tsUnix, 0)
		// 反序列化 JSON 字段。这里忽略 Unmarshal 错误（使用 _），
		// 因为 JSON 格式不正确时字段会保持零值，不影响其他字段的使用。
		json.Unmarshal([]byte(reqJSON), &r.Request)
		json.Unmarshal([]byte(respJSON), &r.Response)
		json.Unmarshal([]byte(tagsJSON), &r.Tags)
		result = append(result, r)
	}
	// rows.Err() 检查迭代过程中是否有隐藏的错误。
	return result, rows.Err()
}

// ---------------------------------------------------------------------------
// 审计日志（Audit Log）相关操作
// ---------------------------------------------------------------------------

// RecordAudit 将一条审计事件持久化到数据库中。
// 审计日志用于记录内容审核的完整链路，包括命中的规则、检测跨度、采取的动作等。
// e.HitsJSON 和 e.SpansJSON 已经是序列化好的 JSON 字符串，直接存入 TEXT 字段。
func (s *sqlStorage) RecordAudit(ctx context.Context, e core.AuditEvent) error {
	// 执行 INSERT 语句，共 10 个字段对应 10 个占位符。
	// e.Timestamp.Unix() 将时间转换为 Unix 时间戳存储，避免时区问题。
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO audits (timestamp, stage, type, hits_json, spans_json, action, provider, model, content_hash, content_preview) VALUES ("+s.placeholders(10)+")",
		e.Timestamp.Unix(), e.Stage, e.Type, e.HitsJSON, e.SpansJSON, e.Action, e.Provider, e.Model, e.ContentHash, e.ContentPreview,
	)
	return err
}

// GetAudits 获取最近的审计日志列表，按时间戳降序排列。
// limit 参数控制返回的最大记录数，若 <= 0 则默认返回 100 条。
// 返回的切片会按时间从新到旧排序，方便前端直接展示。
func (s *sqlStorage) GetAudits(ctx context.Context, limit int) ([]core.AuditEvent, error) {
	// 参数校验：限制默认值为 100，防止不传参时返回过多数据。
	if limit <= 0 {
		limit = 100
	}
	// 查询 audits 表全部字段，按 timestamp 降序排列，通过 LIMIT 限制返回条数。
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, timestamp, stage, type, hits_json, spans_json, action, provider, model, content_hash, content_preview FROM audits ORDER BY timestamp DESC LIMIT "+s.ph(1),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 确保无论成功与否都关闭 rows，释放游标资源。

	var result []core.AuditEvent
	for rows.Next() {
		var e core.AuditEvent
		var tsUnix int64
		// Scan 字段顺序与 SELECT 列顺序严格一致。
		if err := rows.Scan(&e.ID, &tsUnix, &e.Stage, &e.Type, &e.HitsJSON, &e.SpansJSON, &e.Action, &e.Provider, &e.Model, &e.ContentHash, &e.ContentPreview); err != nil {
			return nil, err
		}
		// 将 Unix 时间戳转换为 time.Time 对象。
		e.Timestamp = time.Unix(tsUnix, 0)
		result = append(result, e)
	}
	// rows.Err() 检查迭代过程中是否有隐藏的错误。
	return result, rows.Err()
}

// ---------------------------------------------------------------------------
// 隐私映射（Privacy Mappings）相关操作
// ---------------------------------------------------------------------------

// CreateMapping 将一条隐私数据映射关系持久化到数据库中。
// 每条映射记录一个"原始值"到"假值"的对应关系，
// 在同一个 session 内，相同的原始值始终映射到相同的假值，保证脱敏一致性。
func (s *sqlStorage) CreateMapping(ctx context.Context, m core.PrivacyMapping) error {
	// 执行 INSERT 语句，共 5 个字段对应 5 个占位符。
	// m.CreatedAt.Unix() 将时间转换为 Unix 时间戳存储。
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO privacy_mappings (session_id, original, fake, type, created_at) VALUES ("+s.placeholders(5)+")",
		m.SessionID, m.Original, m.Fake, m.Type, m.CreatedAt.Unix(),
	)
	return err
}

// FindFake 根据 session ID 和原始值查找对应的假值。
// 返回值：
//   - fake：找到的假值字符串
//   - bool：是否找到（true 表示找到，false 表示未找到）
//   - error：数据库错误
//
// 当 QueryRowContext 返回 sql.ErrNoRows 时，说明该映射不存在，返回 ("", false, nil)。
// 这是正常的"未找到"情况，不应视为错误。
func (s *sqlStorage) FindFake(ctx context.Context, sessionID, original string) (string, bool, error) {
	var fake string
	err := s.db.QueryRowContext(ctx,
		// 按 session_id 和 original 两个条件精确匹配查询。
		"SELECT fake FROM privacy_mappings WHERE session_id = "+s.ph(1)+" AND original = "+s.ph(2),
		sessionID, original,
	).Scan(&fake)
	if err == sql.ErrNoRows {
		// 映射不存在是正常情况，返回 false 标识即可。
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return fake, true, nil
}

// FindOriginal 根据 session ID 和假值反向查找对应的原始值。
// 这是 FindFake 的反向操作，用于在需要还原脱敏数据时查找原始值。
// 返回值：
//   - original：找到的原始值字符串
//   - bool：是否找到（true 表示找到，false 表示未找到）
//   - error：数据库错误
func (s *sqlStorage) FindOriginal(ctx context.Context, sessionID, fake string) (string, bool, error) {
	var original string
	err := s.db.QueryRowContext(ctx,
		// 按 session_id 和 fake 两个条件精确匹配查询。
		"SELECT original FROM privacy_mappings WHERE session_id = "+s.ph(1)+" AND fake = "+s.ph(2),
		sessionID, fake,
	).Scan(&original)
	if err == sql.ErrNoRows {
		// 映射不存在是正常情况，返回 false 标识即可。
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return original, true, nil
}

// GetMappings 获取指定 session 下的所有隐私映射关系。
// 结果按 fake 字段长度降序排列（ORDER BY LENGTH(fake) DESC），
// 这样可以确保较长的假值优先被匹配，避免短假值误替换长假值的一部分。
// 例如：假值 "John" 应该比 "Jo" 先被处理，否则 "John" 中的 "Jo" 可能被错误替换。
func (s *sqlStorage) GetMappings(ctx context.Context, sessionID string) ([]core.PrivacyMapping, error) {
	// 按 session_id 过滤，按 fake 长度降序排列。
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, session_id, original, fake, type, created_at FROM privacy_mappings WHERE session_id = "+s.ph(1)+" ORDER BY LENGTH(fake) DESC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // 确保无论成功与否都关闭 rows，释放游标资源。

	var result []core.PrivacyMapping
	for rows.Next() {
		var m core.PrivacyMapping
		var tsUnix int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Original, &m.Fake, &m.Type, &tsUnix); err != nil {
			return nil, err
		}
		// 将 Unix 时间戳转换为 time.Time 对象。
		m.CreatedAt = time.Unix(tsUnix, 0)
		result = append(result, m)
	}
	// rows.Err() 检查迭代过程中是否有隐藏的错误。
	return result, rows.Err()
}

// DeleteMappingsBySession 删除指定 session 下的所有隐私映射关系。
// 当一个 session 结束或用户主动清除脱敏历史时调用此方法。
// 该操作不可逆，请确保调用前已确认用户的删除意图。
func (s *sqlStorage) DeleteMappingsBySession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM privacy_mappings WHERE session_id = "+s.ph(1),
		sessionID,
	)
	return err
}

// CleanupMappings 删除指定时间点之前创建的所有隐私映射关系。
// 用于定期清理过期的映射数据，防止数据库无限膨胀。
// before 参数指定截止时间，所有 created_at 早于该时间的记录都会被删除。
func (s *sqlStorage) CleanupMappings(ctx context.Context, before time.Time) error {
	// before.Unix() 将 time.Time 转换为 Unix 时间戳进行比较。
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM privacy_mappings WHERE created_at < "+s.ph(1),
		before.Unix(),
	)
	return err
}

// Close 关闭底层数据库连接池，释放所有相关资源。
// 应当在程序退出或不再需要该存储实例时调用。
// 关闭后不能再通过该实例执行任何数据库操作。
func (s *sqlStorage) Close() error {
	return s.db.Close()
}

// ph 返回第 n 个位置占位符（1-based），用于构建方言相关的 SQL 语句。
// 它是一个便捷方法，内部调用 placeholder 回调函数。
// 如果 placeholder 未设置（理论上不会发生），默认返回 "?"。
func (s *sqlStorage) ph(n int) string {
	if s.placeholder == nil {
		return "?"
	}
	return s.placeholder(n)
}

// placeholders 返回 n 个逗号分隔的占位符字符串，用于构建 INSERT 语句的 VALUES 部分。
// 例如：
//   - SQLite/MySQL (n=3): "?, ?, ?"
//   - PostgreSQL (n=3):   "$1, $2, $3"
//
// 如果 placeholder 未设置（理论上不会发生），默认使用 "?"。
func (s *sqlStorage) placeholders(n int) string {
	if s.placeholder == nil {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = "?"
		}
		return strings.Join(parts, ", ")
	}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		// 注意：这里传入 i+1 是因为 SQL 占位符从 1 开始编号。
		parts[i] = s.placeholder(i + 1)
	}
	return strings.Join(parts, ", ")
}
