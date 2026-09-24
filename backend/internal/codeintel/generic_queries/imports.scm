;; Generic import/require patterns for Tier B languages.

;; Python imports
(import_statement name: (dotted_name) @import.path) @import.def
(import_from_statement module_name: (dotted_name) @import.path) @import.def

;; JS/TS require
(call_expression
  function: (identifier) @_require (#eq? @_require "require")
  arguments: (arguments (string) @import.path)) @import.def

;; ES module imports
(import_statement source: (string) @import.path) @import.def
(import_declaration source: (string) @import.path) @import.def

;; Rust use
(use_declaration argument: (scoped_identifier) @import.path) @import.def
(use_declaration argument: (identifier) @import.path) @import.def

;; Ruby require
(call
  method: (identifier) @_method (#match? @_method "^require")
  arguments: (argument_list (string) @import.path)) @import.def
