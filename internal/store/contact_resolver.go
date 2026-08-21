package store

import (
	"errors"
	"sort"
	"strings"
	"unicode"
)

type ContactMatch struct {
	Contact Contact `json:"contact"`
	Field   string  `json:"matched_field"`
	Score   int     `json:"score"`
}

type AmbiguousContactError struct {
	Input      string         `json:"input"`
	Candidates []ContactMatch `json:"candidates"`
}

func (err *AmbiguousContactError) Error() string { return "联系人名称存在歧义" }

var ErrContactNotFound = errors.New("联系人不存在")

func normalizeContactName(value string) string {
	return strings.Map(func(current rune) rune {
		if unicode.IsSpace(current) || current == '-' || current == '_' || current == '·' {
			return -1
		}
		return unicode.ToLower(current)
	}, strings.TrimSpace(value))
}

func contactFieldScore(field, value, needle, normalized string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	base := map[string]int{"username": 1000, "alias": 900, "remark": 850, "nickname": 800, "display": 780}[field]
	if strings.EqualFold(value, needle) {
		return base + 100
	}
	normalizedValue := normalizeContactName(value)
	if normalizedValue == normalized {
		return base + 50
	}
	// 单字符和纯标点输入只允许精确匹配，避免一个过短的模糊词被静默
	// 解析成唯一联系人。模糊解析必须至少有两个规范化字符。
	if len([]rune(normalized)) >= 2 && (strings.HasPrefix(normalizedValue, normalized) || strings.HasPrefix(normalized, normalizedValue)) {
		return base
	}
	if len([]rune(normalized)) >= 2 && strings.Contains(normalizedValue, normalized) {
		return base - 100
	}
	return 0
}

func ResolveContact(root, input string) (ContactMatch, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return ContactMatch{}, ErrContactNotFound
	}
	contacts, err := Contacts(root, "", 0)
	if err != nil {
		return ContactMatch{}, err
	}
	normalized := normalizeContactName(input)
	if normalized == "" {
		return ContactMatch{}, ErrContactNotFound
	}
	matches := make([]ContactMatch, 0)
	for _, contact := range contacts {
		fields := []struct{ name, value string }{
			{"username", contact.Username}, {"alias", contact.Alias}, {"remark", contact.Remark},
			{"nickname", contact.Nickname}, {"display", contact.Display},
		}
		best := ContactMatch{Contact: contact}
		for _, field := range fields {
			if score := contactFieldScore(field.name, field.value, input, normalized); score > best.Score {
				best.Score, best.Field = score, field.name
			}
		}
		if best.Score > 0 {
			matches = append(matches, best)
		}
	}
	if len(matches) == 0 {
		return ContactMatch{}, ErrContactNotFound
	}
	sort.Slice(matches, func(left, right int) bool {
		if matches[left].Score == matches[right].Score {
			return matches[left].Contact.Username < matches[right].Contact.Username
		}
		return matches[left].Score > matches[right].Score
	})
	top := matches[0].Score
	var tied []ContactMatch
	for _, match := range matches {
		if match.Score != top {
			break
		}
		tied = append(tied, match)
	}
	if len(tied) > 1 {
		return ContactMatch{}, &AmbiguousContactError{Input: input, Candidates: tied}
	}
	return matches[0], nil
}
