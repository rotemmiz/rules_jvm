package types

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/bazel-contrib/rules_jvm/java/gazelle/private/sorted_set"
)

type PackageName struct {
	Name string
}

func NewPackageName(name string) PackageName {
	return PackageName{
		Name: name,
	}
}

func PackageNameLess(l, r PackageName) bool {
	return l.Name < r.Name
}

func PackageNamesHasPrefix(whole, prefix PackageName) bool {
	w, p := whole.Name, prefix.Name
	return w == p || (len(w) > len(p) && w[len(p)] == '.' && strings.HasPrefix(w, p))
}

type ClassName struct {
	packageName PackageName
	/// bareOuterClassName is the top-most class. i.e. for `com.example.OuterClass.InnerClass.EvenMoreInnerClass`, this will be `OuterClass`.
	bareOuterClassName string
	/// innerClassNames contains all of the class-names nested inside bareOuterClassName, but excluding bareOuterClassName.
	/// i.e. for `com.example.OuterClass.InnerClass.EvenMoreInnerClass`, this will be ["InnerClass", "EvenMoreInnerClass"].
	innerClassNames []string
}

func (c *ClassName) PackageName() PackageName {
	return c.packageName
}

func (c *ClassName) BareOuterClassName() string {
	return c.bareOuterClassName
}

func (c *ClassName) FullyQualifiedOuterClassName() string {
	if c.packageName.Name == "" {
		return c.bareOuterClassName
	}
	return c.packageName.Name + "." + c.bareOuterClassName
}

func (c *ClassName) FullyQualifiedClassName() string {
	if len(c.innerClassNames) == 0 {
		return c.FullyQualifiedOuterClassName()
	}
	n := len(c.packageName.Name) + 1 + len(c.bareOuterClassName)
	for _, inner := range c.innerClassNames {
		n += 1 + len(inner)
	}
	var b strings.Builder
	b.Grow(n)
	if c.packageName.Name != "" {
		b.WriteString(c.packageName.Name)
		b.WriteByte('.')
	}
	b.WriteString(c.bareOuterClassName)
	for _, inner := range c.innerClassNames {
		b.WriteByte('.')
		b.WriteString(inner)
	}
	return b.String()
}

func NewClassName(packageName PackageName, bareOuterClassName string) ClassName {
	return ClassName{
		packageName:        packageName,
		bareOuterClassName: bareOuterClassName,
	}
}

func ParseClassName(fullyQualified string) (*ClassName, error) {
	parts := strings.Split(fullyQualified, ".")
	if len(parts) == 0 {
		return nil, fmt.Errorf("failed to parse class name: empty")
	}

	indexOfOuterClassName := len(parts) - 1
	for i := len(parts) - 1; i >= 0; i-- {
		runes := []rune(parts[i])
		if len(runes) == 0 {
			// Anonymous inner classes end up getting parsed as having name "", so we need to do an "empty" check before looking at the first letter.
			// This means we skip over empty class names when trying to find outer classes.
			continue
		} else if unicode.IsUpper(runes[0]) {
			indexOfOuterClassName = i
		} else {
			break
		}
	}

	packageName := NewPackageName(strings.Join(parts[:indexOfOuterClassName], "."))

	var innerClassNames []string
	if indexOfOuterClassName < (len(parts) - 1) {
		innerClassNames = parts[indexOfOuterClassName+1:]
	}

	return &ClassName{
		packageName:        packageName,
		bareOuterClassName: parts[indexOfOuterClassName],
		innerClassNames:    innerClassNames,
	}, nil
}

// ClassNameLess orders class names by their FullyQualifiedClassName, without building it.
// It is the comparator of every SortedSet[ClassName], so it runs very often.
func ClassNameLess(l, r ClassName) bool {
	return compareFullyQualifiedClassNames(&l, &r) < 0
}

// fqcnCursor walks the bytes of a ClassName's FullyQualifiedClassName without building it.
type fqcnCursor struct {
	c     *ClassName
	part  int // -1: package, 0: outer class, i>0: innerClassNames[i-1]
	off   int
	inSep bool
}

func newFqcnCursor(c *ClassName) fqcnCursor {
	if c.packageName.Name == "" {
		return fqcnCursor{c: c, part: 0}
	}
	return fqcnCursor{c: c, part: -1}
}

func (f *fqcnCursor) partString() string {
	switch {
	case f.part < 0:
		return f.c.packageName.Name
	case f.part == 0:
		return f.c.bareOuterClassName
	default:
		return f.c.innerClassNames[f.part-1]
	}
}

// next returns the next byte, or false at the end of the name.
func (f *fqcnCursor) next() (byte, bool) {
	for {
		if f.inSep {
			f.inSep = false
			return '.', true
		}
		s := f.partString()
		if f.off < len(s) {
			b := s[f.off]
			f.off++
			return b, true
		}
		if f.part >= len(f.c.innerClassNames) {
			return 0, false
		}
		f.part++
		f.off = 0
		f.inSep = true
	}
}

// compareFullyQualifiedClassNames orders like strings.Compare on FullyQualifiedClassName.
func compareFullyQualifiedClassNames(l, r *ClassName) int {
	lc, rc := newFqcnCursor(l), newFqcnCursor(r)
	for {
		lb, lok := lc.next()
		rb, rok := rc.next()
		switch {
		case !lok && !rok:
			return 0
		case !lok:
			return -1
		case !rok:
			return 1
		case lb != rb:
			if lb < rb {
				return -1
			}
			return 1
		}
	}
}

type ResolveInput struct {
	PackageNames         *sorted_set.SortedSet[PackageName]
	ImportedPackageNames *sorted_set.SortedSet[PackageName]
	ImportedClasses      *sorted_set.SortedSet[ClassName]
	ExportedPackageNames *sorted_set.SortedSet[PackageName]
	ExportedClassNames   *sorted_set.SortedSet[ClassName]
	AnnotationProcessors *sorted_set.SortedSet[ClassName]
}

type ResolvableJavaPackage struct {
	packageName PackageName
	isTestOnly  bool
	isTestSuite bool
}

func NewResolvableJavaPackage(packageName PackageName, isTestOnly, isTestSuite bool) *ResolvableJavaPackage {
	return &ResolvableJavaPackage{
		packageName: packageName,
		isTestOnly:  isTestOnly,
		isTestSuite: isTestSuite,
	}
}

func (r *ResolvableJavaPackage) PackageName() PackageName {
	return r.packageName
}

func (r *ResolvableJavaPackage) String() string {
	s := r.packageName.Name
	if r.isTestOnly {
		s += "!testonly"
	}
	if r.isTestSuite {
		s += "!testsuite"
	}
	return s
}

func ParseResolvableJavaPackage(s string) (*ResolvableJavaPackage, error) {
	parts := strings.Split(s, "!")
	if len(parts) > 2 {
		return nil, fmt.Errorf("want 1 or 2 parts separated by !, got: %q", s)
	}
	packageName := NewPackageName(parts[0])
	isTestOnly := false
	isTestSuite := false
	for _, part := range parts[1:] {
		if part == "testonly" {
			isTestOnly = true
		} else if part == "testsuite" {
			isTestSuite = true
		} else {
			return nil, fmt.Errorf("saw unrecognized tag %s", part)
		}
	}
	return &ResolvableJavaPackage{
		packageName: packageName,
		isTestOnly:  isTestOnly,
		isTestSuite: isTestSuite,
	}, nil
}

// LateInit represents a value that can be initialized exactly once.
// It can still be accessed before it's initialized, but once initialized its value cannot change.
// Useful for configuration that "will be initialized at some point, but we're not sure when".
type LateInit[T any] struct {
	value       T
	initialized bool
}

func NewLateInit[T any](valueWhileUninitialized T) *LateInit[T] {
	return &LateInit[T]{
		value:       valueWhileUninitialized,
		initialized: false,
	}
}

func (lib *LateInit[T]) Initialize(value T) {
	if lib.initialized {
		panic("Trying to initialize a LateInit that's already initialized.")
	}
	lib.value = value
	lib.initialized = true
}

func (lib *LateInit[T]) IsInitialized() bool {
	return lib.initialized
}

func (lib *LateInit[T]) Value() T {
	return lib.value
}
