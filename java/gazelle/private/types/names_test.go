package types

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// The reference implementations below are the versions of these functions before they were
// rewritten to avoid allocating. The tests check that the current versions return the same
// results.

func referenceFullyQualifiedOuterClassName(c *ClassName) string {
	var parts []string
	if c.packageName.Name != "" {
		parts = append(parts, strings.Split(c.packageName.Name, ".")...)
	}
	parts = append(parts, c.bareOuterClassName)
	return strings.Join(parts, ".")
}

func referenceFullyQualifiedClassName(c *ClassName) string {
	var parts []string
	if c.packageName.Name != "" {
		parts = append(parts, strings.Split(c.packageName.Name, ".")...)
	}
	parts = append(parts, c.bareOuterClassName)
	parts = append(parts, c.innerClassNames...)
	return strings.Join(parts, ".")
}

func referenceClassNameLess(l, r ClassName) bool {
	return referenceFullyQualifiedClassName(&l) < referenceFullyQualifiedClassName(&r)
}

func referencePackageNamesHasPrefix(whole, prefix PackageName) bool {
	return whole.Name == prefix.Name || strings.HasPrefix(whole.Name, prefix.Name+".")
}

func className(pkg, outer string, inner ...string) ClassName {
	return ClassName{packageName: NewPackageName(pkg), bareOuterClassName: outer, innerClassNames: inner}
}

func TestFullyQualifiedClassName(t *testing.T) {
	for _, tc := range []struct {
		c         ClassName
		wantOuter string
		wantFull  string
	}{
		{className("com.example", "Foo"), "com.example.Foo", "com.example.Foo"},
		{className("", "Foo"), "Foo", "Foo"},
		{className("", "Foo", "Bar"), "Foo", "Foo.Bar"},
		{className("com.example", "Foo", "Bar", "Baz"), "com.example.Foo", "com.example.Foo.Bar.Baz"},
		{className("com", "Foo", ""), "com.Foo", "com.Foo."},
		{className("com..example", "Foo"), "com..example.Foo", "com..example.Foo"},
		{className("com.example.", "Foo"), "com.example..Foo", "com.example..Foo"},
		{className(".", ""), "..", ".."},
		{className("", ""), "", ""},
	} {
		if got := tc.c.FullyQualifiedOuterClassName(); got != tc.wantOuter {
			t.Errorf("FullyQualifiedOuterClassName(%#v) = %q, want %q", tc.c, got, tc.wantOuter)
		}
		if got := tc.c.FullyQualifiedClassName(); got != tc.wantFull {
			t.Errorf("FullyQualifiedClassName(%#v) = %q, want %q", tc.c, got, tc.wantFull)
		}
		if got, want := tc.c.FullyQualifiedOuterClassName(), referenceFullyQualifiedOuterClassName(&tc.c); got != want {
			t.Errorf("FullyQualifiedOuterClassName(%#v) = %q, reference %q", tc.c, got, want)
		}
		if got, want := tc.c.FullyQualifiedClassName(), referenceFullyQualifiedClassName(&tc.c); got != want {
			t.Errorf("FullyQualifiedClassName(%#v) = %q, reference %q", tc.c, got, want)
		}
	}
}

func TestClassNameLess(t *testing.T) {
	for _, tc := range []struct {
		l, r ClassName
		want bool
	}{
		{className("com.example", "Foo"), className("com.example", "Foo"), false},
		{className("com.example", "Foo"), className("com.example", "Goo"), true},
		{className("com.example", "Goo"), className("com.example", "Foo"), false},
		// A prefix sorts first.
		{className("com.example", "Foo"), className("com.example", "Foo", "Bar"), true},
		{className("com.example", "Foo", "Bar"), className("com.example", "Foo"), false},
		{className("com", "Foo"), className("com.example", "Foo"), true},
		// '$' < '.' < '0' < 'A' < '_' < 'a': the separator compares as a byte.
		{className("com.example", "Foo", "Bar"), className("com.example", "Foo$Bar"), false},
		{className("com.example", "Foo$Bar"), className("com.example", "Foo", "Bar"), true},
		{className("com.example", "Foo", "Bar"), className("com.example", "Foo0"), true},
		{className("com.example", "Foo", "Bar"), className("com.example", "FooA"), true},
		// Different splits of the same string are equal.
		{className("com.example", "Foo", "Bar"), className("com", "example", "Foo", "Bar"), false},
		{className("com", "example", "Foo", "Bar"), className("com.example", "Foo", "Bar"), false},
		{className("", "Foo"), className("", "Foo"), false},
		{className("", ""), className("", "A"), true},
		{className("", "A"), className("", ""), false},
		{className("", "Foo", ""), className("", "Foo"), false},
		{className("", "Foo"), className("", "Foo", ""), true},
	} {
		if got := ClassNameLess(tc.l, tc.r); got != tc.want {
			t.Errorf("ClassNameLess(%q, %q) = %v, want %v", tc.l.FullyQualifiedClassName(), tc.r.FullyQualifiedClassName(), got, tc.want)
		}
		if got, want := ClassNameLess(tc.l, tc.r), referenceClassNameLess(tc.l, tc.r); got != want {
			t.Errorf("ClassNameLess(%q, %q) = %v, reference %v", tc.l.FullyQualifiedClassName(), tc.r.FullyQualifiedClassName(), got, want)
		}
	}
}

func TestPackageNamesHasPrefix(t *testing.T) {
	for _, tc := range []struct {
		whole, prefix string
		want          bool
	}{
		{"com.example", "com.example", true},
		{"com.example.foo", "com.example", true},
		{"com.examplefoo", "com.example", false},
		{"com.example", "com.example.foo", false},
		{"com", "", false},
		{".com", "", true},
		{"", "", true},
		{"java.util", "java", true},
		{"javax.inject", "java", false},
		{"com.example..", "com.example.", true},
	} {
		whole, prefix := NewPackageName(tc.whole), NewPackageName(tc.prefix)
		if got := PackageNamesHasPrefix(whole, prefix); got != tc.want {
			t.Errorf("PackageNamesHasPrefix(%q, %q) = %v, want %v", tc.whole, tc.prefix, got, tc.want)
		}
		if got, want := PackageNamesHasPrefix(whole, prefix), referencePackageNamesHasPrefix(whole, prefix); got != want {
			t.Errorf("PackageNamesHasPrefix(%q, %q) = %v, reference %v", tc.whole, tc.prefix, got, want)
		}
	}
}

// randomName returns a short name over a small alphabet, so that random names often share
// prefixes, and that includes the bytes that sort around the '.' separator.
func randomName(rng *rand.Rand, maxLen int) string {
	const alphabet = "$.0Aab_"
	b := make([]byte, rng.Intn(maxLen+1))
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(b)
}

func randomClassName(rng *rand.Rand) ClassName {
	var pkgParts []string
	for i := rng.Intn(4); i > 0; i-- {
		pkgParts = append(pkgParts, randomName(rng, 3))
	}
	var inner []string
	for i := rng.Intn(3); i > 0; i-- {
		inner = append(inner, randomName(rng, 3))
	}
	return className(strings.Join(pkgParts, "."), randomName(rng, 3), inner...)
}

func TestRandomizedEquivalence(t *testing.T) {
	const iterations = 200000
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < iterations; i++ {
		l, r := randomClassName(rng), randomClassName(rng)
		if got, want := l.FullyQualifiedOuterClassName(), referenceFullyQualifiedOuterClassName(&l); got != want {
			t.Fatalf("FullyQualifiedOuterClassName(%#v) = %q, reference %q", l, got, want)
		}
		if got, want := l.FullyQualifiedClassName(), referenceFullyQualifiedClassName(&l); got != want {
			t.Fatalf("FullyQualifiedClassName(%#v) = %q, reference %q", l, got, want)
		}
		if got, want := ClassNameLess(l, r), referenceClassNameLess(l, r); got != want {
			t.Fatalf("ClassNameLess(%#v, %#v) = %v, reference %v", l, r, got, want)
		}
		if got, want := ClassNameLess(l, l), false; got != want {
			t.Fatalf("ClassNameLess(%#v, itself) = %v, want %v", l, got, want)
		}
		whole, prefix := l.PackageName(), r.PackageName()
		if rng.Intn(2) == 0 {
			// Make true results common too.
			if cut := strings.LastIndexByte(whole.Name, '.'); cut >= 0 {
				prefix = NewPackageName(whole.Name[:cut])
			}
		}
		if got, want := PackageNamesHasPrefix(whole, prefix), referencePackageNamesHasPrefix(whole, prefix); got != want {
			t.Fatalf("PackageNamesHasPrefix(%q, %q) = %v, reference %v", whole.Name, prefix.Name, got, want)
		}
	}
}

func TestRandomizedSortOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	names := make([]ClassName, 5000)
	for i := range names {
		names[i] = randomClassName(rng)
	}
	got := append([]ClassName(nil), names...)
	want := append([]ClassName(nil), names...)
	sort.SliceStable(got, func(i, j int) bool { return ClassNameLess(got[i], got[j]) })
	sort.SliceStable(want, func(i, j int) bool { return referenceClassNameLess(want[i], want[j]) })
	for i := range got {
		if got[i].FullyQualifiedClassName() != want[i].FullyQualifiedClassName() {
			t.Fatalf("sorted order differs at %d: %q, reference %q", i, got[i].FullyQualifiedClassName(), want[i].FullyQualifiedClassName())
		}
	}
}

var benchClassNames = []ClassName{
	className("com.example.service.internal.impl", "RequestHandler"),
	className("com.example.service.internal.impl", "RequestHandler", "Builder"),
	className("com.example.service.internal.api", "Response", "Status", "Code"),
	className("org.apache.commons.lang3", "StringUtils"),
}

var benchSink bool
var benchStringSink string

func BenchmarkFullyQualifiedClassName(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchStringSink = benchClassNames[i%len(benchClassNames)].FullyQualifiedClassName()
	}
}

func BenchmarkFullyQualifiedClassNameReference(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchStringSink = referenceFullyQualifiedClassName(&benchClassNames[i%len(benchClassNames)])
	}
}

func BenchmarkFullyQualifiedOuterClassName(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchStringSink = benchClassNames[i%len(benchClassNames)].FullyQualifiedOuterClassName()
	}
}

func BenchmarkFullyQualifiedOuterClassNameReference(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchStringSink = referenceFullyQualifiedOuterClassName(&benchClassNames[i%len(benchClassNames)])
	}
}

func BenchmarkClassNameLess(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSink = ClassNameLess(benchClassNames[i%len(benchClassNames)], benchClassNames[(i+1)%len(benchClassNames)])
	}
}

func BenchmarkClassNameLessReference(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSink = referenceClassNameLess(benchClassNames[i%len(benchClassNames)], benchClassNames[(i+1)%len(benchClassNames)])
	}
}

var benchPackages = []PackageName{
	NewPackageName("com.example.service.internal"),
	NewPackageName("java.util.concurrent"),
	NewPackageName("javax.inject"),
}

var benchPrefixes = []PackageName{
	NewPackageName("java"),
	NewPackageName("com.example"),
}

func BenchmarkPackageNamesHasPrefix(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSink = PackageNamesHasPrefix(benchPackages[i%len(benchPackages)], benchPrefixes[i%len(benchPrefixes)])
	}
}

func BenchmarkPackageNamesHasPrefixReference(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchSink = referencePackageNamesHasPrefix(benchPackages[i%len(benchPackages)], benchPrefixes[i%len(benchPrefixes)])
	}
}
