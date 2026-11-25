package types

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestParseClassName(t *testing.T) {
	for name, tc := range map[string]struct {
		from    string
		want    *ClassName
		wantErr bool
	}{
		"simple": {
			from: "com.example.Simple",
			want: &ClassName{
				packageName:        NewPackageName("com.example"),
				bareOuterClassName: "Simple",
			},
		},
		"no package": {
			from: "Simple",
			want: &ClassName{
				packageName:        NewPackageName(""),
				bareOuterClassName: "Simple",
			},
		},
		"inner": {
			from: "com.example.Simple.Inner",
			want: &ClassName{
				packageName:        NewPackageName("com.example"),
				bareOuterClassName: "Simple",
				innerClassNames:    []string{"Inner"},
			},
		},
		"nested inner": {
			from: "com.example.Simple.Inner.Nested",
			want: &ClassName{
				packageName:        NewPackageName("com.example"),
				bareOuterClassName: "Simple",
				innerClassNames:    []string{"Inner", "Nested"},
			},
		},
		"anonymous inner": {
			from: "com.example.Simple.",
			want: &ClassName{
				packageName:        NewPackageName("com.example"),
				bareOuterClassName: "Simple",
				innerClassNames:    []string{""},
			},
		},
		"gson class": {
			from: "com.google.gson.Gson",
			want: &ClassName{
				packageName:        NewPackageName("com.google.gson"),
				bareOuterClassName: "Gson",
			},
		},
		"guava strings class": {
			from: "com.google.common.base.Strings",
			want: &ClassName{
				packageName:        NewPackageName("com.google.common.base"),
				bareOuterClassName: "Strings",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseClassName(tc.from)
			if tc.wantErr && err != nil {
				t.Fatal("wanted error, got nil error")
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("wanted no error, got %v", err)
				}
				if !reflect.DeepEqual(*tc.want, *got) {
					t.Fatalf("want %v got %v", tc.want, got)
				}
				if tc.from != got.FullyQualifiedClassName() {
					t.Fatalf("Fully Qualified Class Name: want %s got %s", tc.from, got.FullyQualifiedClassName())
				}
			}
		})
	}
}

func className(pkg, outer string, inner ...string) ClassName {
	return ClassName{packageName: NewPackageName(pkg), bareOuterClassName: outer, innerClassNames: inner}
}

func TestFullyQualifiedClassName(t *testing.T) {
	long := strings.Repeat("a", 100)
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
		{className("", ""), "", ""},
		{className(long, long, long), long + "." + long, long + "." + long + "." + long},
	} {
		if got := tc.c.FullyQualifiedOuterClassName(); got != tc.wantOuter {
			t.Errorf("FullyQualifiedOuterClassName(%#v) = %q, want %q", tc.c, got, tc.wantOuter)
		}
		if got := tc.c.FullyQualifiedClassName(); got != tc.wantFull {
			t.Errorf("FullyQualifiedClassName(%#v) = %q, want %q", tc.c, got, tc.wantFull)
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
		// The separator compares as a '.' byte: '$' < '.' < '0'.
		{className("com.example", "Foo", "Bar"), className("com.example", "Foo$Bar"), false},
		{className("com.example", "Foo$Bar"), className("com.example", "Foo", "Bar"), true},
		{className("com.example", "Foo", "Bar"), className("com.example", "Foo0"), true},
		// Different splits of the same name are equal.
		{className("com.example", "Foo", "Bar"), className("com", "example", "Foo", "Bar"), false},
		{className("com", "example", "Foo", "Bar"), className("com.example", "Foo", "Bar"), false},
		{className("", ""), className("", "A"), true},
		{className("", "Foo"), className("", "Foo", ""), true},
	} {
		if got := ClassNameLess(tc.l, tc.r); got != tc.want {
			t.Errorf("ClassNameLess(%q, %q) = %v, want %v", tc.l.FullyQualifiedClassName(), tc.r.FullyQualifiedClassName(), got, tc.want)
		}
	}
}

// TestClassNameEquivalence checks the class name functions against their previous
// implementations, which split the package name and joined the parts.
func TestClassNameEquivalence(t *testing.T) {
	oldFullyQualifiedOuterClassName := func(c ClassName) string {
		var parts []string
		if c.packageName.Name != "" {
			parts = append(parts, strings.Split(c.packageName.Name, ".")...)
		}
		parts = append(parts, c.bareOuterClassName)
		return strings.Join(parts, ".")
	}
	oldFullyQualifiedClassName := func(c ClassName) string {
		var parts []string
		if c.packageName.Name != "" {
			parts = append(parts, strings.Split(c.packageName.Name, ".")...)
		}
		parts = append(parts, c.bareOuterClassName)
		parts = append(parts, c.innerClassNames...)
		return strings.Join(parts, ".")
	}
	// Short names over a small alphabet often share prefixes, and include the bytes that sort
	// around the '.' separator.
	rng := rand.New(rand.NewSource(1))
	randomName := func() string {
		const alphabet = "$.0Aab_"
		b := make([]byte, rng.Intn(4))
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(b)
	}
	randomClassName := func() ClassName {
		var pkg, inner []string
		for i := rng.Intn(4); i > 0; i-- {
			pkg = append(pkg, randomName())
		}
		for i := rng.Intn(3); i > 0; i-- {
			inner = append(inner, randomName())
		}
		return className(strings.Join(pkg, "."), randomName(), inner...)
	}
	for i := 0; i < 200000; i++ {
		l, r := randomClassName(), randomClassName()
		if got, want := l.FullyQualifiedOuterClassName(), oldFullyQualifiedOuterClassName(l); got != want {
			t.Fatalf("FullyQualifiedOuterClassName(%#v) = %q, previously %q", l, got, want)
		}
		if got, want := l.FullyQualifiedClassName(), oldFullyQualifiedClassName(l); got != want {
			t.Fatalf("FullyQualifiedClassName(%#v) = %q, previously %q", l, got, want)
		}
		if got, want := ClassNameLess(l, r), oldFullyQualifiedClassName(l) < oldFullyQualifiedClassName(r); got != want {
			t.Fatalf("ClassNameLess(%#v, %#v) = %v, previously %v", l, r, got, want)
		}
	}
}
