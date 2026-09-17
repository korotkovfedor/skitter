package crawler

import (
	"reflect"
	"testing"

	"golang.org/x/net/html"
)

func TestExtractHrefsPreservesHTMLOrder(t *testing.T) {
	root := &html.Node{Type: html.DocumentNode}
	container := &html.Node{Type: html.ElementNode, Data: "div"}
	first := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: "/first"}}}
	second := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: "/second"}}}
	third := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: "/third"}}}

	root.AppendChild(container)
	container.AppendChild(first)
	container.AppendChild(second)
	root.AppendChild(third)

	want := []string{"/first", "/second", "/third"}
	if got := extractHrefs(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("extractHrefs() = %v, want %v", got, want)
	}
}

func TestExtractHrefsSkipsDescendantsOfLinkedAnchor(t *testing.T) {
	root := &html.Node{Type: html.DocumentNode}
	outer := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: "/outer"}}}
	nested := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: "/nested"}}}
	after := &html.Node{Type: html.ElementNode, Data: "a", Attr: []html.Attribute{{Key: "href", Val: "/after"}}}

	root.AppendChild(outer)
	outer.AppendChild(nested)
	root.AppendChild(after)

	want := []string{"/outer", "/after"}
	if got := extractHrefs(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("extractHrefs() = %v, want %v", got, want)
	}
}
