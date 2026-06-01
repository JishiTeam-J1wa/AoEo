package privacy

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// RuleDatabase holds local filtering rules loaded from a YAML/JSON file.
// It is safe for concurrent reads; use Reload() to update rules at runtime.
type RuleDatabase struct {
	mu            sync.RWMutex
	IPBlocklist   map[string]RuleEntry
	IPAllowlist   map[string]RuleEntry
	IPCIDRBlocks  []*cidrBlock
	DomainBlocklist map[string]RuleEntry
	DomainAllowlist map[string]RuleEntry
	DomainRegexps []*regexp.Regexp
	Keywords      []keywordRule
	RegexRules    []regexRule
}

type cidrBlock struct {
	net  *net.IPNet
	entry RuleEntry
}

type keywordRule struct {
	word  string
	entry RuleEntry
}

type regexRule struct {
	re    *regexp.Regexp
	entry RuleEntry
}

// RuleEntry describes a single rule.
type RuleEntry struct {
	ID          string
	Category    string
	Severity    Severity
	Description string
	Action      Action
}

// RuleScanResult is the output of a rule scan.
type RuleScanResult struct {
	Hits        []RuleHit
	HasBlock    bool
	MaxSeverity Severity
}

// RuleEngine executes rule-based scans.
type RuleEngine struct {
	db *RuleDatabase
}

// NewRuleEngine creates a rule engine backed by the given database.
func NewRuleEngine(db *RuleDatabase) *RuleEngine {
	return &RuleEngine{db: db}
}

// Scan checks the text against all loaded rules.
func (re *RuleEngine) Scan(text string) RuleScanResult {
	if re.db == nil {
		return RuleScanResult{}
	}
	re.db.mu.RLock()
	defer re.db.mu.RUnlock()

	var result RuleScanResult

	// 1. IP addresses
	for _, ip := range extractIPAddresses(text) {
		if hit := re.checkIP(ip); hit != nil {
			result.Hits = append(result.Hits, *hit)
			if hit.Action == ActionBlock {
				result.HasBlock = true
			}
			if hit.Severity > result.MaxSeverity {
				result.MaxSeverity = hit.Severity
			}
		}
	}

	// 2. Domains
	for _, domain := range extractDomains(text) {
		if hit := re.checkDomain(domain); hit != nil {
			result.Hits = append(result.Hits, *hit)
			if hit.Action == ActionBlock {
				result.HasBlock = true
			}
			if hit.Severity > result.MaxSeverity {
				result.MaxSeverity = hit.Severity
			}
		}
	}

	// 3. Keywords
	for _, kw := range re.db.Keywords {
		if strings.Contains(text, kw.word) {
			result.Hits = append(result.Hits, RuleHit{
				Type:        "keyword",
				Matched:     kw.word,
				RuleID:      kw.entry.ID,
				Category:    kw.entry.Category,
				Severity:    kw.entry.Severity,
				Action:      kw.entry.Action,
				Description: kw.entry.Description,
			})
			if kw.entry.Action == ActionBlock {
				result.HasBlock = true
			}
			if kw.entry.Severity > result.MaxSeverity {
				result.MaxSeverity = kw.entry.Severity
			}
		}
	}

	// 4. Regex rules
	for _, rr := range re.db.RegexRules {
		if loc := rr.re.FindStringIndex(text); loc != nil {
			matched := text[loc[0]:loc[1]]
			result.Hits = append(result.Hits, RuleHit{
				Type:        "regex",
				Matched:     matched,
				RuleID:      rr.entry.ID,
				Category:    rr.entry.Category,
				Severity:    rr.entry.Severity,
				Action:      rr.entry.Action,
				Description: rr.entry.Description,
			})
			if rr.entry.Action == ActionBlock {
				result.HasBlock = true
			}
			if rr.entry.Severity > result.MaxSeverity {
				result.MaxSeverity = rr.entry.Severity
			}
		}
	}

	return result
}

func (re *RuleEngine) checkIP(ip string) *RuleHit {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil
	}

	// Check allowlist first (allowlist overrides blocklist).
	if entry, ok := re.db.IPAllowlist[ip]; ok {
		return &RuleHit{
			Type:        "ip",
			Matched:     ip,
			RuleID:      entry.ID,
			Category:    entry.Category,
			Severity:    entry.Severity,
			Action:      entry.Action,
			Description: entry.Description,
		}
	}

	// Check blocklist.
	if entry, ok := re.db.IPBlocklist[ip]; ok {
		return &RuleHit{
			Type:        "ip",
			Matched:     ip,
			RuleID:      entry.ID,
			Category:    entry.Category,
			Severity:    entry.Severity,
			Action:      entry.Action,
			Description: entry.Description,
		}
	}

	// Check CIDR blocks.
	for _, cidr := range re.db.IPCIDRBlocks {
		if cidr.net.Contains(parsed) {
			return &RuleHit{
				Type:        "ip",
				Matched:     ip,
				RuleID:      cidr.entry.ID,
				Category:    cidr.entry.Category,
				Severity:    cidr.entry.Severity,
				Action:      cidr.entry.Action,
				Description: cidr.entry.Description,
			}
		}
	}

	return nil
}

func (re *RuleEngine) checkDomain(domain string) *RuleHit {
	// Strip scheme for matching.
	clean := strings.TrimPrefix(domain, "http://")
	clean = strings.TrimPrefix(clean, "https://")
	clean = strings.TrimSuffix(clean, "/")

	// Extract hostname (remove path).
	host := clean
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}

	// Check allowlist first (try exact, then hostname).
	if entry, ok := re.db.DomainAllowlist[clean]; ok {
		return makeDomainHit(domain, entry)
	}
	if entry, ok := re.db.DomainAllowlist[host]; ok {
		return makeDomainHit(domain, entry)
	}

	// Check blocklist (try exact, then hostname).
	if entry, ok := re.db.DomainBlocklist[clean]; ok {
		return makeDomainHit(domain, entry)
	}
	if entry, ok := re.db.DomainBlocklist[host]; ok {
		return makeDomainHit(domain, entry)
	}

	// Check regex patterns against hostname.
	for _, rePat := range re.db.DomainRegexps {
		if rePat.MatchString(host) {
			return &RuleHit{
				Type:     "domain",
				Matched:  domain,
				RuleID:   "domain-regex",
				Category: "domain_regex",
				Severity: SeverityMedium,
				Action:   ActionMask,
			}
		}
	}

	return nil
}

func makeDomainHit(domain string, entry RuleEntry) *RuleHit {
	return &RuleHit{
		Type:        "domain",
		Matched:     domain,
		RuleID:      entry.ID,
		Category:    entry.Category,
		Severity:    entry.Severity,
		Action:      entry.Action,
		Description: entry.Description,
	}
}

// ---------------------------------------------------------------------------
// YAML loading
// ---------------------------------------------------------------------------

// ruleFile is the top-level YAML structure.
type ruleFile struct {
	Version string `yaml:"version"`
	IPRules struct {
		Blocklist []ipRuleYAML `yaml:"blocklist"`
		Allowlist []ipRuleYAML `yaml:"allowlist"`
		CIDR      []cidrYAML   `yaml:"cidr_blocks"`
	} `yaml:"ip_rules"`
	DomainRules struct {
		Blocklist []domainRuleYAML `yaml:"blocklist"`
		Allowlist []domainRuleYAML `yaml:"allowlist"`
		Regex     []regexYAML      `yaml:"regex_patterns"`
	} `yaml:"domain_rules"`
	Keywords []keywordYAML `yaml:"keyword_rules"`
	Regex    []regexYAML   `yaml:"regex_rules"`
}

type ipRuleYAML struct {
	ID          string `yaml:"id"`
	Value       string `yaml:"value"`
	Category    string `yaml:"category"`
	Severity    string `yaml:"severity"`
	Description string `yaml:"description"`
	Action      string `yaml:"action"`
}

type cidrYAML struct {
	ID          string `yaml:"id"`
	Value       string `yaml:"value"`
	Category    string `yaml:"category"`
	Severity    string `yaml:"severity"`
	Description string `yaml:"description"`
	Action      string `yaml:"action"`
}

type domainRuleYAML struct {
	ID          string `yaml:"id"`
	Value       string `yaml:"value"`
	Category    string `yaml:"category"`
	Severity    string `yaml:"severity"`
	Description string `yaml:"description"`
	Action      string `yaml:"action"`
}

type keywordYAML struct {
	ID          string `yaml:"id"`
	Value       string `yaml:"value"`
	Category    string `yaml:"category"`
	Severity    string `yaml:"severity"`
	Description string `yaml:"description"`
	Action      string `yaml:"action"`
}

type regexYAML struct {
	ID          string `yaml:"id"`
	Pattern     string `yaml:"pattern"`
	Category    string `yaml:"category"`
	Severity    string `yaml:"severity"`
	Description string `yaml:"description"`
	Action      string `yaml:"action"`
}

// LoadRuleDatabase loads rules from a YAML file.
func LoadRuleDatabase(path string) (*RuleDatabase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rule file: %w", err)
	}
	var rf ruleFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parse rule file: %w", err)
	}

	db := &RuleDatabase{
		IPBlocklist:     make(map[string]RuleEntry),
		IPAllowlist:     make(map[string]RuleEntry),
		DomainBlocklist: make(map[string]RuleEntry),
		DomainAllowlist: make(map[string]RuleEntry),
	}

	for _, r := range rf.IPRules.Blocklist {
		db.IPBlocklist[r.Value] = yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action)
	}
	for _, r := range rf.IPRules.Allowlist {
		db.IPAllowlist[r.Value] = yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action)
	}
	for _, r := range rf.IPRules.CIDR {
		_, ipnet, err := net.ParseCIDR(r.Value)
		if err != nil {
			continue // skip invalid CIDR
		}
		db.IPCIDRBlocks = append(db.IPCIDRBlocks, &cidrBlock{
			net:   ipnet,
			entry: yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action),
		})
	}

	for _, r := range rf.DomainRules.Blocklist {
		db.DomainBlocklist[r.Value] = yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action)
	}
	for _, r := range rf.DomainRules.Allowlist {
		db.DomainAllowlist[r.Value] = yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action)
	}
	for _, r := range rf.DomainRules.Regex {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue // skip invalid regex
		}
		db.DomainRegexps = append(db.DomainRegexps, re)
	}

	for _, r := range rf.Keywords {
		db.Keywords = append(db.Keywords, keywordRule{
			word:  r.Value,
			entry: yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action),
		})
	}

	for _, r := range rf.Regex {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			continue // skip invalid regex
		}
		db.RegexRules = append(db.RegexRules, regexRule{
			re:    re,
			entry: yamlToEntry(r.ID, r.Category, r.Severity, r.Description, r.Action),
		})
	}

	return db, nil
}

func yamlToEntry(id, category, severity, description, action string) RuleEntry {
	return RuleEntry{
		ID:          id,
		Category:    category,
		Severity:    parseSeverity(severity),
		Description: description,
		Action:      parseAction(action),
	}
}

func parseSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "low":
		return SeverityLow
	case "medium":
		return SeverityMedium
	case "high":
		return SeverityHigh
	case "critical":
		return SeverityCritical
	default:
		return SeverityMedium
	}
}

func parseAction(s string) Action {
	switch strings.ToLower(s) {
	case "block":
		return ActionBlock
	case "mask":
		return ActionMask
	case "pseudonymize":
		return ActionPseudonymize
	case "audit":
		return ActionAudit
	default:
		return ActionMask
	}
}
