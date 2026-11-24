package store

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Tag limits.
const (
	MaxTagLen  = 24 // characters
	MaxTagsPer = 12 // tags on one user
)

// NormalizeTag tidies one tag: spaces collapsed, no commas (Latin or
// Persian), at most MaxTagLen characters. "" when nothing is left.
func NormalizeTag(t string) string {
	t = strings.Join(strings.Fields(strings.NewReplacer(",", " ", "،", " ").Replace(t)), " ")
	if utf8.RuneCountInString(t) > MaxTagLen {
		t = strings.TrimSpace(string([]rune(t)[:MaxTagLen]))
	}
	return t
}

// NormalizeTags tidies a user's tags: each one tidied, the same tag in
// another case kept once (the first spelling wins), at most MaxTagsPer.
func NormalizeTags(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = NormalizeTag(t)
		k := strings.ToLower(t)
		if t == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
		if len(out) == MaxTagsPer {
			break
		}
	}
	return out
}

// HasTag says whether u carries tag (case does not matter).
func (u *User) HasTag(tag string) bool {
	tag = strings.ToLower(NormalizeTag(tag))
	for _, t := range u.Tags {
		if strings.ToLower(t) == tag {
			return true
		}
	}
	return false
}

// AddTag puts tag on u (once); false when it was there or u is full.
func (u *User) AddTag(tag string) bool {
	tag = NormalizeTag(tag)
	if tag == "" || u.HasTag(tag) || len(u.Tags) >= MaxTagsPer {
		return false
	}
	u.Tags = append(u.Tags, tag)
	return true
}

// RemoveTag takes tag off u; false when it was not there.
func (u *User) RemoveTag(tag string) bool {
	tag = strings.ToLower(NormalizeTag(tag))
	for i, t := range u.Tags {
		if strings.ToLower(t) == tag {
			u.Tags = append(u.Tags[:i:i], u.Tags[i+1:]...)
			return true
		}
	}
	return false
}

// TagCount is a tag and how many users carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// CountTags lists the tags of users, the most used first.
func CountTags(users []*User) []TagCount {
	count := map[string]int{}
	spelling := map[string]string{}
	for _, u := range users {
		for _, t := range u.Tags {
			k := strings.ToLower(t)
			if _, ok := spelling[k]; !ok {
				spelling[k] = t
			}
			count[k]++
		}
	}
	out := []TagCount{}
	for k, n := range count {
		out = append(out, TagCount{spelling[k], n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Tag) < strings.ToLower(out[j].Tag)
	})
	return out
}
