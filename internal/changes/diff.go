package changes

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Diff builds the diff of what a run touched: diff against HEAD of the
// modified files, content of the new files and git show of the commits.
// With no changes and no commits it returns "" without error.
func Diff(dir string, r Result) (string, error) {
	if len(r.Files) == 0 && len(r.Commits) == 0 && r.Next == nil {
		return "", nil
	}
	if dir == "" {
		return "", fmt.Errorf("no dir")
	}
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root = strings.TrimSpace(root)

	var b strings.Builder

	for _, a := range r.Files {
		if !a.New {
			// A file git cannot compare does not hide the rest of the diff.
			out, err := git(root, "diff", "--no-color", "HEAD", "--", a.Path)
			if err != nil {
				out = "diff --git a/" + a.Path + " b/" + a.Path + "\n(git could not compare it: " + err.Error() + ")\n"
			}
			if out != "" {
				b.WriteString(out)
				if !strings.HasSuffix(out, "\n") {
					b.WriteString("\n")
				}
			}
			continue
		}

		b.WriteString("diff --git a/" + a.Path + " b/" + a.Path + "\n")
		b.WriteString("new file\n")

		p := filepath.Join(root, filepath.FromSlash(a.Path))
		f, err := os.Open(p)
		if err != nil {
			b.WriteString("(can no longer be read: it may have been deleted later)\n")
			continue
		}
		const maxBytes = 256 * 1024
		data, err := io.ReadAll(io.LimitReader(f, maxBytes))
		f.Close()
		if err != nil {
			return "", err
		}

		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			b.WriteString("(binary)\n")
		} else if len(data) > 0 {
			text := strings.ReplaceAll(string(data), "\r", "")
			if strings.HasSuffix(text, "\n") {
				text = strings.TrimSuffix(text, "\n")
			}
			for _, l := range strings.Split(text, "\n") {
				b.WriteString("+" + l + "\n")
			}
		}
	}

	for _, c := range r.Commits {
		out, err := git(root, "show", "--no-color", "--stat", "-p", c.Hash)
		if err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("run commit\n")
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
	}

	if r.Next != nil {
		out, err := git(root, "show", "--no-color", "--stat", "-p", r.Next.Hash)
		if err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("next commit (may contain the work)\n")
		b.WriteString(out)
		if !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
	}

	if b.Len() == 0 {
		return "", nil
	}
	return b.String(), nil
}
