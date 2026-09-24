package codeintel

import "log/slog"

// Incremental type-hierarchy inference: the production-DB analogue of Pass 8.
//
// The full pipeline builds hierarchy in memory (pass8_type_hierarchy.go); a single
// saved file never runs the pipeline, so these functions do the same work with SQL
// against code.db. Two producers of the same relationship is deliberate here —
// they run in mutually exclusive paths — unlike the deleted Pass 4 heuristic, which
// ran in the *same* pass sequence as Pass 8 and only served to reintroduce the
// cross-package vendor/_test.go matches Pass 8 filters out.

// InferImplementsFromDB runs structural satisfaction inference on the production DB
// for incremental indexing. After a file delta, checks if modified types now implement
// known interfaces.
func InferImplementsFromDB(ci *CodeIndex, filePaths []string) int {
	if ci == nil || !ci.hasReader() {
		return 0
	}

	db := ci.writerDB
	if db == nil {
		return 0
	}

	type ifaceRow struct {
		ifaceID   int64
		ifaceName string
		method    string
		pkg       string
	}
	rows, err := ci.readerDB.Query(`
		SELECT i.id, i.name, m.name, i.package_path
		FROM symbols i
		JOIN symbols m ON m.parent = i.name AND m.package_path = i.package_path
			AND m.kind IN ('function', 'method')
		WHERE i.kind = 'interface'`)
	if err != nil {
		return 0
	}
	defer rows.Close()

	type ifaceInfo struct {
		id      int64
		name    string
		pkg     string
		methods map[string]struct{}
	}
	ifaceMap := make(map[int64]*ifaceInfo)
	for rows.Next() {
		var r ifaceRow
		if rows.Scan(&r.ifaceID, &r.ifaceName, &r.method, &r.pkg) != nil {
			continue
		}
		if ifaceMap[r.ifaceID] == nil {
			ifaceMap[r.ifaceID] = &ifaceInfo{id: r.ifaceID, name: r.ifaceName, pkg: r.pkg, methods: make(map[string]struct{})}
		}
		ifaceMap[r.ifaceID].methods[r.method] = struct{}{}
	}

	if len(ifaceMap) == 0 {
		return 0
	}

	var count int
	for _, fp := range filePaths {
		typeRows, err := ci.readerDB.Query(`
			SELECT t.id, t.name, t.package_path FROM symbols t
			WHERE t.file_path = ? AND t.kind IN ('type', 'class')`, IndexPath(fp))
		if err != nil {
			continue
		}

		type typeRow struct {
			id   int64
			name string
			pkg  string
		}
		var types []typeRow
		for typeRows.Next() {
			var t typeRow
			if typeRows.Scan(&t.id, &t.name, &t.pkg) == nil {
				types = append(types, t)
			}
		}
		typeRows.Close()

		for _, t := range types {
			methRows, err := ci.readerDB.Query(`
				SELECT name FROM symbols
				WHERE parent = ? AND package_path = ? AND kind IN ('function', 'method')`, t.name, t.pkg)
			if err != nil {
				continue
			}
			myMethods := make(map[string]struct{})
			for methRows.Next() {
				var m string
				if methRows.Scan(&m) == nil {
					myMethods[m] = struct{}{}
				}
			}
			methRows.Close()

			if len(myMethods) == 0 {
				continue
			}

			for _, iface := range ifaceMap {
				if len(iface.methods) == 0 {
					continue
				}
				if subset(iface.methods, myMethods) {
					var exists bool
					ci.readerDB.QueryRow(`
						SELECT 1 FROM edges WHERE source_id = ? AND target_id = ? AND kind = 'implements'`,
						t.id, iface.id).Scan(&exists)
					if exists {
						continue
					}
					// `inferred`, bound: both ids came from the symbol table, but
					// the relation is a conclusion drawn from two method sets.
					// Passed as a parameter, not spelled inline, so the value
					// cannot drift from the enum the CHECK is generated from.
					db.Exec(`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source, metadata)
						VALUES (?, ?, ?, 'implements', ?, 'method-set-satisfaction', '{"source":"method_set_satisfaction"}')`,
						t.id, iface.id, iface.name, ResolutionInferred)
					count++

					// Derive OVERRIDES edges for the matched type.
					inferOverridesFromDB(ci, t.id, t.name, t.pkg, iface.id, iface.name, iface.pkg)
				}
			}
		}
	}

	if count > 0 {
		slog.Info("incremental: implements edges inferred", "count", count, "files", len(filePaths))
	}
	return count
}

// inferOverridesFromDB creates OVERRIDES edges for a concrete type that implements an interface.
func inferOverridesFromDB(ci *CodeIndex, concreteTypeID int64, concreteName, concretePkg string, ifaceID int64, ifaceName, ifacePkg string) {
	db := ci.writerDB

	// Get interface method IDs.
	ifaceMethodRows, err := ci.readerDB.Query(`
		SELECT id, name FROM symbols
		WHERE parent = ? AND package_path = ? AND kind IN ('function', 'method')`,
		ifaceName, ifacePkg)
	if err != nil {
		return
	}
	defer ifaceMethodRows.Close()

	type methodInfo struct {
		id   int64
		name string
	}
	var ifaceMethods []methodInfo
	for ifaceMethodRows.Next() {
		var m methodInfo
		if ifaceMethodRows.Scan(&m.id, &m.name) == nil {
			ifaceMethods = append(ifaceMethods, m)
		}
	}

	for _, im := range ifaceMethods {
		var concreteMethodID int64
		ci.readerDB.QueryRow(`
			SELECT id FROM symbols
			WHERE parent = ? AND package_path = ? AND name = ? AND kind IN ('function', 'method')`,
			concreteName, concretePkg, im.name).Scan(&concreteMethodID)
		if concreteMethodID == 0 {
			continue
		}

		var exists bool
		ci.readerDB.QueryRow(`
			SELECT 1 FROM edges WHERE source_id = ? AND target_id = ? AND kind = 'overrides'`,
			concreteMethodID, im.id).Scan(&exists)
		if exists {
			continue
		}

		db.Exec(`INSERT INTO edges (source_id, target_id, target_name, kind, resolution, source, metadata)
			VALUES (?, ?, ?, 'overrides', ?, 'overrides-derivation', '{"derived_from":"implements"}')`,
			concreteMethodID, im.id, im.name, ResolutionInferred)
	}
}

func subset(a, b map[string]struct{}) bool {
	if len(a) > len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// AnnotateVirtualCallsDB is the incremental version of Pass 9.
// It marks CALLS edges targeting interface methods as virtual (metadata update)
// and creates sole-implementor resolved edges on the production DB.
func AnnotateVirtualCallsDB(ci *CodeIndex) {
	if ci == nil || !ci.hasReader() || ci.writerDB == nil {
		return
	}
	db := ci.writerDB

	// Find all interface method IDs.
	rows, err := ci.readerDB.Query(`
		SELECT m.id FROM symbols m
		JOIN symbols i ON m.parent = i.name AND m.package_path = i.package_path
		WHERE m.kind IN ('function', 'method') AND i.kind = 'interface'`)
	if err != nil {
		return
	}
	defer rows.Close()

	ifaceMethodIDs := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ifaceMethodIDs[id] = true
		}
	}
	if len(ifaceMethodIDs) == 0 {
		return
	}

	// Mark call edges targeting interface methods as virtual.
	var annotated int
	for id := range ifaceMethodIDs {
		res, err := db.Exec(`UPDATE edges SET metadata = '{"virtual":true}'
			WHERE kind = 'call' AND target_id = ? AND (metadata = '' OR metadata IS NULL)`, id)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				annotated += int(n)
			}
		}
	}

	if annotated > 0 {
		slog.Info("incremental: virtual call annotation", "annotated", annotated)
	}
}

// hasInterfaceDefinition checks if any of the given file paths contain interface definitions.
func hasInterfaceDefinition(ci *CodeIndex, paths []string) bool {
	if ci == nil || !ci.hasReader() || len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		var count int
		ci.readerDB.QueryRow(`SELECT COUNT(*) FROM symbols WHERE file_path = ? AND kind = 'interface'`, IndexPath(p)).Scan(&count)
		if count > 0 {
			return true
		}
	}
	return false
}

// listAllTypeFiles returns all file paths that contain type/class/struct definitions.
func listAllTypeFiles(ci *CodeIndex) []string {
	if ci == nil || !ci.hasReader() {
		return nil
	}
	rows, err := ci.readerDB.Query(`SELECT DISTINCT file_path FROM symbols WHERE kind IN ('type', 'class')`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			files = append(files, f)
		}
	}
	return files
}
