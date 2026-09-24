package codeintel

import (
	"context"
	"log/slog"
	"strings"
)

// pass8TypeHierarchy extracts type hierarchy edges (IMPLEMENTS + OVERRIDES).
// Three stages:
//   - Stage A: Explicit declaration extraction (AST-derived, all languages with explicit syntax)
//   - Stage B: Implicit satisfaction (Go method-set check, TS structural optional)
//   - Stage C: OVERRIDES derivation (automatic from IMPLEMENTS)
//
// Returns (implements_count, overrides_count).
func (p *Pipeline) pass8TypeHierarchy(_ context.Context) (int, int) {
	p.buf.mu.Lock()
	defer p.buf.mu.Unlock()

	// Stage A: uses explicit declarations already extracted by Pass 3 workers
	// (tree-sitter queries for implements/extends keywords).
	// Pass 3 workers already produce EdgeExtends edges for explicit inheritance;
	// we upgrade them to EdgeImplements where the target is an interface.
	implCount := p.stageAExplicitDeclarations()

	// Stage B: Go-style structural interface satisfaction (method-set ⊆ check).
	implCount += p.stageBMethodSetSatisfaction()

	// Stage C: Derive OVERRIDES edges from IMPLEMENTS edges.
	overrideCount := p.stageCDeriveOverrides()

	if implCount > 0 || overrideCount > 0 {
		slog.Info("pass8: type hierarchy complete",
			"implements", implCount, "overrides", overrideCount)
	}
	return implCount, overrideCount
}

// stageAExplicitDeclarations upgrades extends edges to implements where target is interface,
// and produces new IMPLEMENTS edges from any explicit syntax detected by Pass 3.
// Must be called with buf.mu held.
func (p *Pipeline) stageAExplicitDeclarations() int {
	count := 0
	for i := range p.buf.Edges {
		e := &p.buf.Edges[i]
		if e.Kind != EdgeExtends {
			continue
		}
		targetNode := p.buf.Nodes[e.TargetQName]
		if targetNode != nil && targetNode.Kind == NodeInterface {
			e.Kind = EdgeImplements
			// Resolution is deliberately untouched: this stage reclassifies the
			// relationship, it does not re-look-up the target. Whatever Pass 4
			// concluded about the binding is still all we know — stamping
			// `exact` here would claim an inferred binding proved itself
			// because the node it landed on happened to be an interface.
			e.Source = "explicit-declaration"
			e.Metadata = `{"source":"explicit_declaration"}`
			count++
		}
	}
	return count
}

// stageBMethodSetSatisfaction runs structural interface satisfaction:
// for each interface I with methods M_I, for each concrete type T with methods M_T,
// if M_I ⊆ M_T and no IMPLEMENTS(T→I) edge exists yet, emit one.
// Edges are `inferred`: the endpoints are real buffer nodes, but the relation is
// a conclusion drawn from two method sets, not a declaration in the source.
// Must be called with buf.mu held.
func (p *Pipeline) stageBMethodSetSatisfaction() int {
	type ifaceInfo struct {
		qname   string
		methods map[string]struct{}
	}

	// Map name → []qname to handle same-name interfaces in different packages.
	interfaceQNames := make(map[string][]string)
	for qname, n := range p.buf.Nodes {
		if n.Kind == NodeInterface {
			interfaceQNames[n.Name] = append(interfaceQNames[n.Name], qname)
		}
	}

	ifaceMethods := make(map[string]map[string]struct{})
	for _, n := range p.buf.Nodes {
		if (n.Kind == NodeFunction || n.Kind == NodeMethod) && n.Parent != "" {
			for _, ifaceQName := range interfaceQNames[n.Parent] {
				ifaceNode := p.buf.Nodes[ifaceQName]
				if ifaceNode != nil && n.PackagePath == ifaceNode.PackagePath {
					if ifaceMethods[ifaceQName] == nil {
						ifaceMethods[ifaceQName] = make(map[string]struct{})
					}
					ifaceMethods[ifaceQName][n.Name] = struct{}{}
				}
			}
		}
	}

	// Resolve embedded interfaces: if interface A embeds interface B (via extends or implements edge
	// where both source and target are interfaces), merge B's methods into A's method set.
	// This handles Go's `type ReadWriter interface { Reader; Writer }`.
	for _, e := range p.buf.Edges {
		if e.Kind != EdgeExtends && e.Kind != EdgeImplements {
			continue
		}
		sourceNode := p.buf.Nodes[e.SourceQName]
		targetNode := p.buf.Nodes[e.TargetQName]
		if sourceNode == nil || targetNode == nil {
			continue
		}
		if sourceNode.Kind == NodeInterface && targetNode.Kind == NodeInterface {
			if ifaceMethods[e.SourceQName] == nil {
				ifaceMethods[e.SourceQName] = make(map[string]struct{})
			}
			for m := range ifaceMethods[e.TargetQName] {
				ifaceMethods[e.SourceQName][m] = struct{}{}
			}
		}
	}
	// Second pass: resolve by name for same-package embedded interfaces without explicit extends edge.
	for qname, n := range p.buf.Nodes {
		if n.Kind != NodeInterface {
			continue
		}
		children := p.buf.ChildMethods(n.Name, n.PackagePath)
		for _, child := range children {
			if child.Signature != "" && !strings.Contains(child.Signature, "(") {
				embeddedName := strings.TrimSpace(child.Name)
				for _, embQName := range interfaceQNames[embeddedName] {
					embNode := p.buf.Nodes[embQName]
					if embNode != nil && embNode.PackagePath == n.PackagePath {
						if ifaceMethods[qname] == nil {
							ifaceMethods[qname] = make(map[string]struct{})
						}
						for m := range ifaceMethods[embQName] {
							ifaceMethods[qname][m] = struct{}{}
						}
					}
				}
			}
		}
	}

	var interfaces []ifaceInfo
	for qname, methods := range ifaceMethods {
		if len(methods) == 0 {
			continue
		}
		interfaces = append(interfaces, ifaceInfo{qname: qname, methods: methods})
	}

	if len(interfaces) == 0 {
		return 0
	}

	typeByName := make(map[string][]string)
	for qname, n := range p.buf.Nodes {
		if n.Kind == NodeType || n.Kind == NodeClass {
			typeByName[n.Name] = append(typeByName[n.Name], qname)
		}
	}

	typeMethods := make(map[string]map[string]struct{})
	for _, n := range p.buf.Nodes {
		if (n.Kind == NodeFunction || n.Kind == NodeMethod) && n.Parent != "" {
			if _, isIface := interfaceQNames[n.Parent]; isIface {
				continue
			}
			for _, tqname := range typeByName[n.Parent] {
				tn := p.buf.Nodes[tqname]
				if tn != nil && tn.PackagePath == n.PackagePath {
					if typeMethods[tqname] == nil {
						typeMethods[tqname] = make(map[string]struct{})
					}
					typeMethods[tqname][n.Name] = struct{}{}
				}
			}
		}
	}

	// Any IMPLEMENTS edge already in the buffer wins: at this point the only
	// producer that ran is Stage A (explicit declaration, which is ground truth,
	// not inference). This used to read `Certainty > 0.6` to mean "not from Pass 4"
	// — a float literal standing in for provenance, so retuning any confidence
	// constant would have silently changed which edges got superseded. Provenance
	// belongs in `Source`/`Metadata`, and the question "did someone already claim
	// this pair" needs no confidence at all.
	existingImpl := make(map[[2]string]bool)
	for _, e := range p.buf.Edges {
		if e.Kind == EdgeImplements {
			existingImpl[[2]string{e.SourceQName, e.TargetQName}] = true
		}
	}

	var count int
	for _, iface := range interfaces {
		if len(iface.methods) == 0 {
			continue
		}
		ifaceNode := p.buf.Nodes[iface.qname]
		ifacePkg := ""
		if ifaceNode != nil {
			ifacePkg = ifaceNode.PackagePath
		}

		for tqname, tMethods := range typeMethods {
			if existingImpl[[2]string{tqname, iface.qname}] {
				continue
			}
			// Performance: skip if method count is already insufficient.
			if len(tMethods) < len(iface.methods) {
				continue
			}
			// Performance: prefer same-package matches, skip obvious non-production files.
			tn := p.buf.Nodes[tqname]
			if tn != nil && ifacePkg != "" && tn.PackagePath != ifacePkg {
				if strings.Contains(tn.PackagePath, "/vendor/") ||
					strings.HasSuffix(tn.FilePath, "_test.go") {
					continue
				}
			}
			if subset(iface.methods, tMethods) {
				p.buf.Edges = append(p.buf.Edges, Edge{
					SourceQName: tqname,
					TargetQName: iface.qname,
					TargetName:  p.buf.Nodes[iface.qname].Name,
					Kind:        EdgeImplements,
					Resolution:  ResolutionInferred,
					Source:      "method-set-satisfaction",
					Metadata:    `{"source":"method_set_satisfaction"}`,
				})
				existingImpl[[2]string{tqname, iface.qname}] = true
				count++
			}
		}
	}
	return count
}

// stageCDeriveOverrides derives OVERRIDES edges from IMPLEMENTS edges:
// For each (ConcreteType --IMPLEMENTS--> Interface), for each Interface method M,
// find matching ConcreteType.M and emit ConcreteType.M --OVERRIDES--> Interface.M.
// Must be called with buf.mu held.
func (p *Pipeline) stageCDeriveOverrides() int {
	var implEdges []Edge
	for _, e := range p.buf.Edges {
		if e.Kind == EdgeImplements {
			implEdges = append(implEdges, e)
		}
	}

	if len(implEdges) == 0 {
		return 0
	}

	// Build lookup: (parentName, pkgPath, methodName) → QualifiedName
	type methodKey struct {
		parent string
		pkg    string
		name   string
	}
	methodIndex := make(map[methodKey]string)
	for qname, n := range p.buf.Nodes {
		if n.Kind == NodeFunction || n.Kind == NodeMethod {
			if n.Parent != "" {
				methodIndex[methodKey{parent: n.Parent, pkg: n.PackagePath, name: n.Name}] = qname
			}
		}
	}

	var count int
	existingOverrides := make(map[[2]string]bool)
	for _, e := range p.buf.Edges {
		if e.Kind == EdgeOverrides {
			existingOverrides[[2]string{e.SourceQName, e.TargetQName}] = true
		}
	}

	for _, impl := range implEdges {
		// An override is only as true as the implements edge it is derived from,
		// so an unbound parent has nothing to derive against. Skipping is not
		// defensive padding: propagating a non-bound resolution onto an edge
		// whose endpoints do resolve is precisely the producer contradiction
		// flushResolution refuses, and it would fail the whole run.
		if !impl.Resolution.Bound() {
			continue
		}
		concreteNode := p.buf.Nodes[impl.SourceQName]
		ifaceNode := p.buf.Nodes[impl.TargetQName]
		if concreteNode == nil || ifaceNode == nil {
			continue
		}

		ifaceMethods := p.buf.ChildMethods(ifaceNode.Name, ifaceNode.PackagePath)
		for _, ifaceMethod := range ifaceMethods {
			concreteMethodQName := methodIndex[methodKey{
				parent: concreteNode.Name,
				pkg:    concreteNode.PackagePath,
				name:   ifaceMethod.Name,
			}]
			if concreteMethodQName == "" {
				continue
			}
			key := [2]string{concreteMethodQName, ifaceMethod.QualifiedName}
			if existingOverrides[key] {
				continue
			}
			p.buf.Edges = append(p.buf.Edges, Edge{
				SourceQName: concreteMethodQName,
				TargetQName: ifaceMethod.QualifiedName,
				TargetName:  ifaceMethod.Name,
				Kind:        EdgeOverrides,
				// Inherited, not restated: this pair is true exactly as far as
				// the implements edge is. A declared implements makes the
				// override a fact of the language; a method-set inference makes
				// it an inference.
				Resolution: impl.Resolution,
				Source:     "overrides-derivation",
				Metadata:   `{"derived_from":"implements"}`,
			})
			existingOverrides[key] = true
			count++
		}
	}
	return count
}
