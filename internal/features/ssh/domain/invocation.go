// Package domain holds the ssh rules gitswitch relies on: reading the
// command line git gives ssh, where each host keeps its SSH keys page, and
// reading a host's login greeting. It also declares what the ssh use cases
// need from the outside world (ports). It is pure. Import it as ssh.
package domain

import "strings"

// Invocation is what git asked ssh to do.
type Invocation struct {
	Args     []string // original args, passed to ssh untouched
	Host     string   // e.g. github.com
	RepoPath string   // e.g. acme-corp/api.git
	Probe    bool     // git's "-G" variant probe: never prompt
	Batch    bool     // -o BatchMode=yes: nobody is there to answer
}

// batchMode reports an ssh option "BatchMode=yes" (or "BatchMode yes").
func batchMode(opt string) bool {
	k, v, ok := strings.Cut(strings.TrimSpace(opt), "=")
	if !ok {
		k, v, ok = strings.Cut(strings.TrimSpace(opt), " ")
	}
	return ok && strings.EqualFold(strings.TrimSpace(k), "BatchMode") && strings.EqualFold(strings.TrimSpace(v), "yes")
}

// ssh options that take a value (from ssh(1)).
const argOpts = "BbcDEeFIiJLlmOoPpQRSWw"

// Parse reads git's ssh command line.
func Parse(args []string) Invocation {
	inv := Invocation{Args: args}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = args[i+1:]
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if strings.Contains(a[1:], "G") && !strings.ContainsAny(a[1:2], argOpts) {
				inv.Probe = true
			}
			if a == "-o" && i+1 < len(args) && batchMode(args[i+1]) || strings.HasPrefix(a, "-o") && batchMode(a[2:]) {
				inv.Batch = true
			}
			if len(a) == 2 && strings.ContainsRune(argOpts, rune(a[1])) {
				i++ // skip the value
			}
			continue
		}
		rest = args[i:]
		break
	}
	if len(rest) == 0 {
		return inv
	}
	dest := rest[0]
	if i := strings.LastIndex(dest, "@"); i >= 0 {
		dest = dest[i+1:]
	}
	inv.Host = strings.ToLower(dest)
	inv.RepoPath = RepoPathFromCommand(strings.Join(rest[1:], " "))
	return inv
}

// RepoPathFromCommand pulls owner/repo.git out of "git-upload-pack 'owner/repo.git'".
func RepoPathFromCommand(cmd string) string {
	i, j := strings.Index(cmd, "'"), strings.LastIndex(cmd, "'")
	var p string
	if i >= 0 && j > i {
		p = strings.ReplaceAll(cmd[i+1:j], `'\''`, `'`)
	} else if f := strings.Fields(cmd); len(f) >= 2 {
		p = strings.Trim(f[len(f)-1], `"`)
	}
	return strings.TrimLeft(strings.TrimPrefix(p, "~"), "/")
}

// DestArgs keeps port/user/config options and the destination, dropping
// the remote command, for an `ssh -G` lookup.
func DestArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if len(a) == 2 && strings.ContainsRune(argOpts, rune(a[1])) && i+1 < len(args) {
				if a == "-p" || a == "-l" || a == "-F" || a == "-o" {
					out = append(out, a, args[i+1])
				}
				i++
			}
			continue
		}
		return append(out, a)
	}
	return out
}
