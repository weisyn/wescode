package codeintel

// BackgroundCompleter was removed (dead scaffold — no call site existed).
// Pipeline and incremental indexing are driven by:
//   - engine.backgroundIndexAll() → RunWithoutDump per root + MergeFrom +
//     one DumpAndReopen (full multi-root index on startup)
//   - codeintel.FileWatcher → flushPending() → IndexFilesViaWorker
//     (incremental row-level delta; never a full-pipeline DB replacement)
//
// The former BackgroundCompleter duplicated these responsibilities without
// being wired into any code path. If a Salsa-style async completion layer
// is needed in the future, it should integrate with FileWatcher's event
// loop rather than running a parallel timer.
