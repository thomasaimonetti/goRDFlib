package shacl

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/tggo/goRDFlib/graph"
	"github.com/tggo/goRDFlib/term"
)

// NewGraphFromRDFTriples indexes the caller's triples instead of reading the
// store: the graph must read exactly like NewGraphFromRDF over the same filled
// store (every lookup form, Len, Triples, namespaces, the report of Core and
// SPARQL shapes), including when the index is built while another goroutine is
// still filling the store (run with -race).
func TestNewGraphFromRDFTriplesReadsLikeNewGraphFromRDF(t *testing.T) {
	ctx := context.Background()
	ex := func(s string) term.URIRef { return term.NewURIRefUnsafe("http://example.org/" + s) }
	typ := term.NewURIRefUnsafe(RDF + "type")
	var ts []term.Triple
	for i := range 200 {
		n := ex(fmt.Sprintf("i%d", i))
		ts = append(ts, term.Triple{Subject: n, Predicate: typ, Object: ex("Item")})
		if i%7 != 3 {
			ts = append(ts, term.Triple{Subject: n, Predicate: ex("size"), Object: term.NewLiteral(fmt.Sprint(i%23), term.WithDatatype(term.NewURIRefUnsafe(XSD+"integer")))})
		}
		ts = append(ts, term.Triple{Subject: n, Predicate: ex("label"), Object: term.NewLiteral(fmt.Sprintf("item %d", i), term.WithLang("en"))})
		ts = append(ts, term.Triple{Subject: ex("box"), Predicate: ex("holds"), Object: n})
		if i%5 == 0 {
			b := term.NewBNode(fmt.Sprintf("b%d", i))
			ts = append(ts, term.Triple{Subject: n, Predicate: ex("part"), Object: b}, term.Triple{Subject: b, Predicate: typ, Object: ex("Part")})
		}
	}
	shapes := `@prefix sh: <http://www.w3.org/ns/shacl#> . @prefix ex: <http://example.org/> .
ex:S a sh:NodeShape ; sh:targetClass ex:Item ;
  sh:property [ sh:path ex:size ; sh:minCount 1 ; sh:maxInclusive 20 ] ;
  sh:property [ sh:path ex:part ; sh:maxCount 0 ] ;
  sh:sparql [ sh:select """PREFIX ex: <http://example.org/>
    SELECT $this ?value WHERE { $this ex:size ?value FILTER (?value > 17) }""" ] .
ex:B a sh:NodeShape ; sh:targetNode ex:box ; sh:property [ sh:path ex:holds ; sh:class ex:Item ; sh:maxCount 150 ] .`
	sg, err := LoadTurtleString(shapes, "http://example.org/shapes")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := CompileShapes(sg)
	if err != nil {
		t.Fatal(err)
	}
	fill := func(rg *graph.Graph) {
		for _, x := range ts {
			if err := rg.Add(ctx, x.Subject, x.Predicate, x.Object); err != nil {
				t.Error(err)
			}
		}
	}
	ref := graph.NewGraph()
	fill(ref)
	want, err := NewGraphFromRDF(ctx, ref, "")
	if err != nil {
		t.Fatal(err)
	}
	norm := func(r ValidationReport) string {
		var out []string
		for _, x := range r.Results {
			out = append(out, x.FocusNode.TermKey()+" "+x.Value.TermKey()+" "+x.SourceConstraintComponent.Value()+" "+x.ResultPath.TermKey())
		}
		sort.Strings(out)
		return strings.Join(out, "\n")
	}
	sortedTriples := func(g *Graph, s, p, o *Term) string {
		var out []string
		for _, x := range g.All(s, p, o) {
			out = append(out, x.Subject.TermKey()+" "+x.Predicate.TermKey()+" "+x.Object.TermKey())
		}
		sort.Strings(out)
		return strings.Join(out, "\n")
	}
	wantReport := norm(cs.Validate(want))
	if !strings.Contains(wantReport, "MinCount") || !strings.Contains(wantReport, "SPARQLConstraint") || !strings.Contains(wantReport, "MaxCount") {
		t.Fatalf("the reference report misses a constraint kind:\n%s", wantReport)
	}
	check := func(name string, got *Graph) {
		if g, w := norm(cs.Validate(got)), wantReport; g != w {
			t.Errorf("%s: report differs:\n got %s\nwant %s", name, g, w)
		}
		if got.Len() != want.Len() || len(got.Triples()) != len(want.Triples()) {
			t.Errorf("%s: Len %d / %d triples, want %d / %d", name, got.Len(), len(got.Triples()), want.Len(), len(want.Triples()))
		}
		i5, box, size, holds, item := IRI("http://example.org/i5"), IRI("http://example.org/box"), IRI("http://example.org/size"), IRI("http://example.org/holds"), IRI("http://example.org/Item")
		rt := IRI(RDF + "type")
		lit := Literal("5", XSD+"integer", "")
		for _, q := range [][3]*Term{{nil, nil, nil}, {&i5, nil, nil}, {nil, &size, nil}, {nil, &rt, &item}, {&box, &holds, nil},
			{&i5, &size, &lit}, {nil, nil, &item}, {&i5, nil, &item}} {
			if g, w := sortedTriples(got, q[0], q[1], q[2]), sortedTriples(want, q[0], q[1], q[2]); g != w {
				t.Errorf("%s: All(%v, %v, %v) differs", name, q[0], q[1], q[2])
			}
		}
		if len(got.namespaces()) != len(want.namespaces()) {
			t.Errorf("%s: %d namespace bindings, want %d", name, len(got.namespaces()), len(want.namespaces()))
		}
	}
	// store filled first
	rg := graph.NewGraph()
	fill(rg)
	g1, err := NewGraphFromRDFTriples(ctx, rg, ts, "")
	if err != nil {
		t.Fatal(err)
	}
	check("filled first", g1)
	// index built while the store is being filled
	for range 8 {
		rg := graph.NewGraph()
		var wg sync.WaitGroup
		wg.Go(func() { fill(rg) })
		g2, err := NewGraphFromRDFTriples(ctx, rg, ts, "")
		wg.Wait()
		if err != nil {
			t.Fatal(err)
		}
		check("concurrent fill", g2)
	}
	if _, err := NewGraphFromRDFTriples(ctx, nil, ts, ""); err == nil {
		t.Error("nil rdflib graph accepted")
	}
}
