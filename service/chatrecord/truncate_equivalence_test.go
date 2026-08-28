package chatrecord

import (
	"strings"
	"testing"
)

// naiveTruncate is the spelling Truncate used to have. Keeping it here lets the
// faster version be checked against it rather than against a hand-written list
// of expectations, which is what a refactor like this actually needs to prove.
func naiveTruncate(s string, max int) string {
	s = storable(s)
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

func naiveClip(s string, max int) string {
	if max <= 0 {
		return ""
	}
	s = storable(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func TestTruncateAndClipStillCutWhereTheyUsedTo(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"短",
		"登录不上去了请看截图",
		strings.Repeat("我", 50),
		strings.Repeat("ab", 50),
		"emoji 也是多字节 🙂🙂🙂 结尾",
		"混合 mixed 中英 123 ✅",
		"带空字节\x00的内容",
		"截断的多字节\xbb尾巴",
		strings.Repeat("字\x00", 30),
	}
	for _, in := range inputs {
		for _, max := range []int{-1, 0, 1, 2, 3, 9, 10, 11, 49, 50, 51, 1000} {
			if got, want := Truncate(in, max), naiveTruncate(in, max); got != want {
				t.Errorf("Truncate(%q, %d) = %q, 旧实现给的是 %q", in, max, got, want)
			}
			if got, want := clip(in, max), naiveClip(in, max); got != want {
				t.Errorf("clip(%q, %d) = %q, 旧实现给的是 %q", in, max, got, want)
			}
		}
	}
}

// A reply that arrived at the buffer ceiling is the case worth measuring: the
// old spelling converted all of it to runes before keeping 32000.
func BenchmarkTruncateALargeReply(b *testing.B) {
	big := strings.Repeat("这是一段中文回复内容。", 1<<20/10)
	b.Run("now", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Truncate(big, 32000)
		}
	})
	b.Run("before", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = naiveTruncate(big, 32000)
		}
	})
}
