// Package builtin holds wisp's built-in tools, one file each: read, ls,
// glob, grep, todo, write, edit, multi_edit, bash, fetch, and web_search.
//
// Shared helpers are grouped by concern: args.go decodes arguments,
// files.go reads lines and writes files safely, search.go walks trees for
// glob and grep, and sensitive.go decides which paths hold credentials,
// which reading asks about first.
package builtin
