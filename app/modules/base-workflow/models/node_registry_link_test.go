package models_test

// The builtin node definitions are registered by service/nodes (plan T93), which imports this
// package, so internal tests cannot import it. An external test file in the same directory can:
// linking it here fills the registry for every test in this package's test binary.
import _ "git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
