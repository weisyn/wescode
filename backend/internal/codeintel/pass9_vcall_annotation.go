package codeintel

import (
	"context"
	"log/slog"
)

// pass9VCallAnnotation annotates CALLS edges that target interface methods as virtual,
// and resolves sole-implementor virtual calls to concrete methods.
// Returns the number of virtual calls annotated.
func (p *Pipeline) pass9VCallAnnotation(_ context.Context) int {
	p.buf.mu.Lock()
	defer p.buf.mu.Unlock()

	// Collect all interface method QualifiedNames.
	interfaceMethodQNames := make(map[string]bool)
	interfaceQNs := make(map[string][]string) // name → []qname
	for qname, n := range p.buf.Nodes {
		if n.Kind == NodeInterface {
			interfaceQNs[n.Name] = append(interfaceQNs[n.Name], qname)
		}
	}
	for qname, n := range p.buf.Nodes {
		if (n.Kind == NodeFunction || n.Kind == NodeMethod) && n.Parent != "" {
			for _, ifaceQName := range interfaceQNs[n.Parent] {
				ifaceNode := p.buf.Nodes[ifaceQName]
				if ifaceNode != nil && n.PackagePath == ifaceNode.PackagePath {
					interfaceMethodQNames[qname] = true
				}
			}
		}
	}

	if len(interfaceMethodQNames) == 0 {
		return 0
	}

	// Annotate CALLS edges targeting interface methods as virtual.
	var vcallCount int
	for i := range p.buf.Edges {
		e := &p.buf.Edges[i]
		if e.Kind != EdgeCall {
			continue
		}
		if interfaceMethodQNames[e.TargetQName] {
			e.Metadata = `{"virtual":true}`
			vcallCount++
		}
	}

	// Sole-implementor optimization: when an interface has exactly one implementor,
	// create additional resolved CALLS edges from virtual callers to concrete methods.
	soleImplCount := p.resolveSoleImplementors(interfaceMethodQNames)

	if vcallCount > 0 || soleImplCount > 0 {
		slog.Info("pass9: virtual call annotation",
			"virtual_calls", vcallCount, "sole_impl_resolved", soleImplCount)
	}
	return vcallCount
}

// resolveSoleImplementors finds interfaces with exactly one implementor and creates
// resolved CALLS edges from virtual callers to the concrete method.
// Must be called with buf.mu held.
func (p *Pipeline) resolveSoleImplementors(interfaceMethodQNames map[string]bool) int {
	// Count implementors per interface (by target QName of IMPLEMENTS edges).
	implCountByIface := make(map[string][]string) // iface QName → []concrete QNames
	for _, e := range p.buf.Edges {
		if e.Kind == EdgeImplements {
			implCountByIface[e.TargetQName] = append(implCountByIface[e.TargetQName], e.SourceQName)
		}
	}

	// Build method lookup for concrete types.
	type methodKey struct {
		parent string
		pkg    string
		name   string
	}
	methodIndex := make(map[methodKey]string)
	for qname, n := range p.buf.Nodes {
		if (n.Kind == NodeFunction || n.Kind == NodeMethod) && n.Parent != "" {
			methodIndex[methodKey{parent: n.Parent, pkg: n.PackagePath, name: n.Name}] = qname
		}
	}

	var resolvedCount int
	for ifaceQName, impls := range implCountByIface {
		if len(impls) != 1 {
			continue
		}
		soleImplQName := impls[0]
		soleImplNode := p.buf.Nodes[soleImplQName]
		if soleImplNode == nil {
			continue
		}

		ifaceNode := p.buf.Nodes[ifaceQName]
		if ifaceNode == nil {
			continue
		}

		ifaceMethods := p.buf.ChildMethods(ifaceNode.Name, ifaceNode.PackagePath)
		for _, ifaceMethod := range ifaceMethods {
			if !interfaceMethodQNames[ifaceMethod.QualifiedName] {
				continue
			}

			concreteMethodQName := methodIndex[methodKey{
				parent: soleImplNode.Name,
				pkg:    soleImplNode.PackagePath,
				name:   ifaceMethod.Name,
			}]
			if concreteMethodQName == "" {
				continue
			}

			// Find all virtual callers of the interface method — collect first, then append.
			var resolved []Edge
			for _, e := range p.buf.Edges {
				if e.Kind == EdgeCall && e.TargetQName == ifaceMethod.QualifiedName {
					resolved = append(resolved, Edge{
						SourceQName: e.SourceQName,
						TargetQName: concreteMethodQName,
						TargetName:  ifaceMethod.Name,
						Kind:        EdgeCall,
						// `inferred`: the concrete method is a real node, but
						// the reason this call reaches it is that the interface
						// has exactly one implementor today. Add a second one
						// and this edge becomes wrong — that contingency is
						// what separates it from an `exact` static call.
						Resolution: ResolutionInferred,
						Source:     "sole-implementor-resolution",
						Metadata:   `{"resolved_from":"sole_implementor","virtual_target":"` + ifaceMethod.QualifiedName + `"}`,
					})
				}
			}
			p.buf.Edges = append(p.buf.Edges, resolved...)
			resolvedCount += len(resolved)
		}
	}
	return resolvedCount
}
