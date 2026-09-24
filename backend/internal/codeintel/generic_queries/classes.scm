;; Generic class/type definition patterns for Tier B languages.

;; Class definitions (covers: Python, JS/TS, Ruby, Dart, Scala, etc.)
(class_definition name: (identifier) @class.name) @class.def
(class_declaration name: (identifier) @class.name) @class.def
(class_declaration name: (type_identifier) @class.name) @class.def

;; Struct/record definitions (covers: Rust, Elixir, etc.)
(struct_item name: (type_identifier) @class.name) @class.def
(struct_expression name: (type_identifier) @class.name) @class.def

;; Module definitions (covers: Elixir, Ruby, Python, etc.)
(module_definition name: (constant) @class.name) @class.def
(module name: (identifier) @class.name) @class.def

;; Interface/trait definitions
(interface_declaration name: (identifier) @class.name) @class.def
(trait_item name: (type_identifier) @class.name) @class.def

;; Enum definitions
(enum_item name: (type_identifier) @class.name) @class.def
(enum_definition name: (identifier) @class.name) @class.def
