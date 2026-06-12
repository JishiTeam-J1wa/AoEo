// generator.go 为各实体类型生成逼真的伪造数据，确保生成值外观合理但完全虚构。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"unicode"
)

// FakeGenerator 为各实体类型生成逼真的伪造数据。
// 保证生成值外观合理但完全虚构。
type FakeGenerator struct {
	rnd *rand.Rand
	mu  sync.Mutex
}

// NewFakeGenerator 使用指定种子创建新的生成器。
// 测试时使用固定种子以获得确定性结果，生产环境使用 time.Now().UnixNano() 获得随机性。
//
// Param:
//   - seed: int64 - 随机数种子
//
// Return:
//   - *FakeGenerator: 初始化完成的伪造值生成器
func NewFakeGenerator(seed int64) *FakeGenerator {
	return &FakeGenerator{rnd: rand.New(rand.NewSource(seed))}
}

// Generate 根据实体类型返回对应的伪造值。
//
// Param:
//   - typ: EntityType - 敏感数据的实体类型
//   - original: string - 原始敏感值，部分类型会据此保持格式一致性
//
// Return:
//   - string: 生成的伪造替换值
func (g *FakeGenerator) Generate(typ EntityType, original string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch typ {
	case EntityIP:
		return g.fakeIP()
	case EntityDomain:
		return g.fakeDomain(original)
	case EntityPerson:
		return g.fakeName(original)
	case EntityPhone:
		return g.fakePhone(original)
	case EntityIDCard:
		return g.fakeIDCard()
	case EntitySecret:
		return g.fakeSecret(original)
	case EntityAddress:
		return g.fakeAddress()
	case EntityEmail:
		return g.fakeEmail(original)
	case EntityURL:
		return g.fakeURL(original)
	case EntityDate:
		return g.fakeDate()
	default:
		return g.genericMask(original)
	}
}

// fakeIP 生成 RFC1918 私有 IP 地址，避免与真实公网 IP 冲突。
func (g *FakeGenerator) fakeIP() string {
	switch g.rnd.Intn(3) {
	case 0:
		return fmt.Sprintf("10.%d.%d.%d", g.rnd.Intn(256), g.rnd.Intn(256), g.rnd.Intn(256))
	case 1:
		return fmt.Sprintf("172.%d.%d.%d", 16+g.rnd.Intn(16), g.rnd.Intn(256), g.rnd.Intn(256))
	default:
		return fmt.Sprintf("192.168.%d.%d", g.rnd.Intn(256), g.rnd.Intn(256))
	}
}

// fakeDomain 生成伪造域名，保留原始子域名层级和顶级域名。
func (g *FakeGenerator) fakeDomain(original string) string {
	parts := strings.Split(original, ".")
	if len(parts) >= 2 {
		suffix := parts[len(parts)-1]
		if suffix == "com" || suffix == "cn" || suffix == "net" || suffix == "org" {
			return fmt.Sprintf("internal-%s.masked.%s", g.randomSuffix(6), suffix)
		}
	}
	return fmt.Sprintf("masked-%s.local", g.randomSuffix(6))
}

// fakeName 根据原始值是否包含 CJK 字符，生成中文或英文伪造姓名。
func (g *FakeGenerator) fakeName(original string) string {
	hasCJK := false
	for _, r := range original {
		if unicode.Is(unicode.Han, r) {
			hasCJK = true
			break
		}
	}
	if hasCJK {
		return g.fakeChineseName()
	}
	return g.fakeEnglishName()
}

// fakeChineseName 从常见中文姓名库中随机组合姓氏和名字。
func (g *FakeGenerator) fakeChineseName() string {
	surnames := []string{"李", "王", "张", "刘", "陈", "杨", "赵", "黄", "周", "吴", "徐", "孙", "马", "朱", "胡"}
	names := []string{"伟", "芳", "娜", "敏", "静", "丽", "强", "磊", "军", "洋", "勇", "艳", "杰", "娟", "涛"}
	return surnames[g.rnd.Intn(len(surnames))] + names[g.rnd.Intn(len(names))]
}

// fakeEnglishName 从常见英文姓名库中随机组合名和姓。
func (g *FakeGenerator) fakeEnglishName() string {
	first := []string{"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda"}
	last := []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis"}
	return first[g.rnd.Intn(len(first))] + " " + last[g.rnd.Intn(len(last))]
}

// fakePhone 生成伪造电话号码。
// 如果原始值匹配中国大陆手机号格式（11 位且以 1 开头），则保持该格式。
func (g *FakeGenerator) fakePhone(original string) string {
	if len(original) == 11 && original[0] == '1' {
		prefixes := []string{"138", "139", "135", "136", "137", "150", "151", "152", "157", "158", "159", "186", "187", "188"}
		prefix := prefixes[g.rnd.Intn(len(prefixes))]
		return prefix + fmt.Sprintf("%08d", g.rnd.Intn(100000000))
	}
	// 通用路径：保持长度不变，替换所有数字字符
	var b strings.Builder
	for _, c := range original {
		if unicode.IsDigit(c) {
			b.WriteByte(byte('0' + g.rnd.Intn(10)))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// fakeIDCard 生成合法的 18 位中国大陆身份证号码，包含正确的校验位。
func (g *FakeGenerator) fakeIDCard() string {
	areas := []string{"110101", "310101", "440106", "500101", "510107", "330106", "420106"}
	area := areas[g.rnd.Intn(len(areas))]
	year := 1970 + g.rnd.Intn(40)
	month := 1 + g.rnd.Intn(12)
	day := 1 + g.rnd.Intn(28)
	seq := g.rnd.Intn(1000)
	base := fmt.Sprintf("%s%04d%02d%02d%03d", area, year, month, day, seq)
	return base + computeIDCheckDigit(base)
}

// fakeSecret 生成伪造密钥/令牌，保持原始值的长度和字符类分布。
func (g *FakeGenerator) fakeSecret(original string) string {
	var b strings.Builder
	for _, c := range original {
		switch {
		case unicode.IsDigit(c):
			b.WriteByte(byte('0' + g.rnd.Intn(10)))
		case unicode.IsUpper(c):
			b.WriteByte(byte('A' + g.rnd.Intn(26)))
		case unicode.IsLower(c):
			b.WriteByte(byte('a' + g.rnd.Intn(26)))
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// fakeAddress 生成中国大陆格式的伪造地址。
func (g *FakeGenerator) fakeAddress() string {
	cities := []string{"北京市", "上海市", "广州市", "深圳市", "成都市", "杭州市"}
	districts := []string{"朝阳区", "海淀区", "天河区", "南山区", "武侯区", "西湖区"}
	streets := []string{"建国路", "人民大道", "解放街", "中山路", "建设大道", "和平路"}
	return fmt.Sprintf("%s%s%s%d号%d室",
		cities[g.rnd.Intn(len(cities))],
		districts[g.rnd.Intn(len(districts))],
		streets[g.rnd.Intn(len(streets))],
		100+g.rnd.Intn(900),
		1+g.rnd.Intn(30),
	)
}

// fakeEmail 生成伪造邮箱地址，尽量保留原始域名部分。
func (g *FakeGenerator) fakeEmail(original string) string {
	parts := strings.Split(original, "@")
	if len(parts) == 2 {
		return fmt.Sprintf("user-%s@%s", g.randomSuffix(6), parts[1])
	}
	return fmt.Sprintf("user-%s@example.com", g.randomSuffix(6))
}

// fakeURL 生成伪造 URL，保持与原始值相同的协议类型。
func (g *FakeGenerator) fakeURL(original string) string {
	if strings.HasPrefix(original, "https://") {
		return fmt.Sprintf("https://internal-%s.masked.local/path", g.randomSuffix(6))
	}
	return fmt.Sprintf("http://internal-%s.masked.local/path", g.randomSuffix(6))
}

// fakeDate 生成 YYYY-MM-DD 格式的伪造日期。
func (g *FakeGenerator) fakeDate() string {
	year := 1980 + g.rnd.Intn(40)
	month := 1 + g.rnd.Intn(12)
	day := 1 + g.rnd.Intn(28)
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// genericMask 使用星号遮蔽原始值，保持长度不变。
func (g *FakeGenerator) genericMask(original string) string {
	return strings.Repeat("*", len(original))
}

// randomSuffix 生成指定长度的随机小写字母和数字组合，用于构造唯一标识。
func (g *FakeGenerator) randomSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[g.rnd.Intn(len(letters))]
	}
	return string(b)
}

// computeIDCheckDigit 计算 18 位身份证号码的末位校验码（基于前 17 位）。
func computeIDCheckDigit(base17 string) string {
	if len(base17) != 17 {
		return "X"
	}
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checkCodes := []string{"1", "0", "X", "9", "8", "7", "6", "5", "4", "3", "2"}

	sum := 0
	for i := 0; i < 17; i++ {
		digit := int(base17[i] - '0')
		sum += digit * weights[i]
	}
	return string(checkCodes[sum%11])
}
