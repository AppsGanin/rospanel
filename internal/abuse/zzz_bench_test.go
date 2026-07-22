package abuse

import (
	"fmt"
	"testing"
)

func makeList(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%d.%d.%d.0/24", 10+i/65536%200, i/256%256, i%256))
	}
	return out
}

func BenchmarkMatchMissIP(b *testing.B) {
	m := New()
	m.SetIP(CatBadIP, makeList(50000))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Match("8.8.8.8")
	}
}

func BenchmarkMatchDomain(b *testing.B) {
	m := New()
	m.SetIP(CatBadIP, makeList(50000))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Match("www.example.com")
	}
}

func BenchmarkSetIP50k(b *testing.B) {
	entries := makeList(50000)
	m := New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.SetIP(CatCustom, entries)
	}
}
