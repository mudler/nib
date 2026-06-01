package plugin

import (
	"bufio"
	"bytes"
	"strings"
)

// splitFrontmatter separates a markdown file's leading `--- ... ---` YAML
// frontmatter from its body. If there is no frontmatter, fm is empty and body
// is the whole input.
func splitFrontmatter(data []byte) (fm []byte, body string) {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !s.Scan() {
		return nil, string(data)
	}
	if strings.TrimRight(s.Text(), "\r") != "---" {
		return nil, string(data)
	}
	var fmBuf, bodyBuf bytes.Buffer
	inBody := false
	for s.Scan() {
		line := s.Text()
		if !inBody && strings.TrimRight(line, "\r") == "---" {
			inBody = true
			continue
		}
		if inBody {
			bodyBuf.WriteString(line)
			bodyBuf.WriteString("\n")
		} else {
			fmBuf.WriteString(line)
			fmBuf.WriteString("\n")
		}
	}
	if !inBody {
		return nil, string(data)
	}
	return fmBuf.Bytes(), bodyBuf.String()
}
