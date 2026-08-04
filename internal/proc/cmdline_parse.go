package proc

import "bytes"

// parseProcArgs2 extracts argv from a kern.procargs2 buffer (Darwin).
// Layout after the leading 4-byte nargs int has been stripped:
//
//	exec_path \0 [padding \0...] argv[0] \0 ... argv[nargs-1] \0 envp...
//
// Empty argv elements within the nargs count are preserved. The result is
// joined with spaces by callers to match Linux ReadCmdline.
func parseProcArgs2(args []byte, nargs int) []string {
	chunks := bytes.Split(args, []byte{0})
	if len(chunks) <= 1 {
		return nil
	}
	// Skip exec_path (chunks[0]) and any padding NULs before argv[0].
	i := 1
	for ; i < len(chunks) && len(chunks[i]) == 0; i++ {
	}
	if nargs > len(chunks)-i {
		nargs = len(chunks) - i
	}
	if nargs < 0 {
		nargs = 0
	}
	out := make([]string, 0, nargs)
	for ; nargs > 0; nargs-- {
		out = append(out, string(chunks[i]))
		i++
	}
	return out
}

// joinArgv joins argv with spaces (Linux cmdline representation).
func joinArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	var b bytes.Buffer
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(a)
	}
	return b.String()
}
