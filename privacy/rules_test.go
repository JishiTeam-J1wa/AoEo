package privacy

import (
	"net"
	"regexp"
	"testing"
)

func TestRuleEngine_IPBlocklist(t *testing.T) {
	db := &RuleDatabase{
		IPBlocklist: map[string]RuleEntry{
			"191.1.1.1": {ID: "ip-001", Category: "attack_ip", Severity: SeverityCritical, Action: ActionBlock},
		},
		IPAllowlist:   make(map[string]RuleEntry),
		DomainBlocklist: make(map[string]RuleEntry),
		DomainAllowlist: make(map[string]RuleEntry),
	}
	re := NewRuleEngine(db)

	// Blocked IP
	result := re.Scan("攻击来源是 191.1.1.1")
	if !result.HasBlock {
		t.Fatal("expected block for attack IP")
	}
	if len(result.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(result.Hits))
	}

	// Unknown IP
	result2 := re.Scan("服务器在 8.8.8.8")
	if result2.HasBlock {
		t.Fatal("should not block unknown IP")
	}
}

func TestRuleEngine_IPAllowlist(t *testing.T) {
	db := &RuleDatabase{
		IPBlocklist: map[string]RuleEntry{
			"192.168.1.1": {ID: "ip-002", Category: "internal", Severity: SeverityHigh, Action: ActionMask},
		},
		IPAllowlist: map[string]RuleEntry{
			"192.168.1.1": {ID: "ip-allow-001", Category: "safe", Severity: SeverityLow, Action: ActionAudit},
		},
		DomainBlocklist: make(map[string]RuleEntry),
		DomainAllowlist: make(map[string]RuleEntry),
	}
	re := NewRuleEngine(db)

	// Allowlist should take precedence over blocklist.
	result := re.Scan("IP 192.168.1.1")
	if result.HasBlock {
		t.Fatal("allowlist should override blocklist")
	}
	if len(result.Hits) != 1 || result.Hits[0].Action != ActionAudit {
		t.Fatalf("expected audit action, got %+v", result.Hits)
	}
}

func TestRuleEngine_CIDRBlock(t *testing.T) {
	_, ipnet, _ := net.ParseCIDR("10.0.0.0/8")
	db := &RuleDatabase{
		IPBlocklist:     make(map[string]RuleEntry),
		IPAllowlist:     make(map[string]RuleEntry),
		IPCIDRBlocks:    []*cidrBlock{{net: ipnet, entry: RuleEntry{ID: "cidr-001", Category: "internal", Severity: SeverityMedium, Action: ActionMask}}},
		DomainBlocklist: make(map[string]RuleEntry),
		DomainAllowlist: make(map[string]RuleEntry),
	}
	re := NewRuleEngine(db)

	result := re.Scan("内网服务器 10.5.6.7")
	if len(result.Hits) != 1 {
		t.Fatalf("expected 1 hit for CIDR match, got %d", len(result.Hits))
	}
}

func TestRuleEngine_DomainBlocklist(t *testing.T) {
	db := &RuleDatabase{
		IPBlocklist:     make(map[string]RuleEntry),
		IPAllowlist:     make(map[string]RuleEntry),
		DomainBlocklist: map[string]RuleEntry{
			"www.x1.com": {ID: "domain-001", Category: "internal_domain", Severity: SeverityHigh, Action: ActionMask},
		},
		DomainAllowlist: make(map[string]RuleEntry),
	}
	re := NewRuleEngine(db)

	result := re.Scan("访问 https://www.x1.com/api")
	if len(result.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(result.Hits))
	}
	if result.Hits[0].Matched != "https://www.x1.com/api" {
		t.Fatalf("unexpected match: %s", result.Hits[0].Matched)
	}
}

func TestRuleEngine_Keyword(t *testing.T) {
	db := &RuleDatabase{
		IPBlocklist:     make(map[string]RuleEntry),
		IPAllowlist:     make(map[string]RuleEntry),
		DomainBlocklist: make(map[string]RuleEntry),
		DomainAllowlist: make(map[string]RuleEntry),
		Keywords: []keywordRule{
			{word: "机密", entry: RuleEntry{ID: "kw-001", Category: "confidential", Severity: SeverityHigh, Action: ActionBlock}},
		},
	}
	re := NewRuleEngine(db)

	result := re.Scan("这是一份机密文件")
	if !result.HasBlock {
		t.Fatal("expected block for keyword")
	}
	if result.Hits[0].Matched != "机密" {
		t.Fatalf("unexpected match: %s", result.Hits[0].Matched)
	}
}

func TestRuleEngine_Regex(t *testing.T) {
	rePat, _ := regexp.Compile(`[A-Z]{2}\d{8}`)
	db := &RuleDatabase{
		IPBlocklist:     make(map[string]RuleEntry),
		IPAllowlist:     make(map[string]RuleEntry),
		DomainBlocklist: make(map[string]RuleEntry),
		DomainAllowlist: make(map[string]RuleEntry),
		RegexRules: []regexRule{
			{re: rePat, entry: RuleEntry{ID: "regex-001", Category: "employee_id", Severity: SeverityMedium, Action: ActionMask}},
		},
	}
	re := NewRuleEngine(db)

	result := re.Scan("工号 HR12345678 已激活")
	if len(result.Hits) != 1 {
		t.Fatalf("expected 1 regex hit, got %d", len(result.Hits))
	}
	if result.Hits[0].Matched != "HR12345678" {
		t.Fatalf("unexpected match: %s", result.Hits[0].Matched)
	}
}
