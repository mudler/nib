package extraction

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"path"
)

// zipFiles builds a name→File map for quick lookups.
func zipFiles(zr *zip.ReadCloser) map[string]*zip.File {
	m := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		m[f.Name] = f
	}
	return m
}

// zipOpenPath opens a file in the zip by cleaned path name.
func zipOpenPath(files map[string]*zip.File, name string) io.ReadCloser {
	name = path.Clean(name)
	f, ok := files[name]
	if !ok {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	return rc
}

// parseOOXMLRels parses an OOXML .rels file and returns a map of Id→Target.
func parseOOXMLRels(files map[string]*zip.File, relsPath string) map[string]string {
	m := make(map[string]string)
	rc := zipOpenPath(files, relsPath)
	if rc == nil {
		return m
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			var id, target string
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "Id":
					id = a.Value
				case "Target":
					target = a.Value
				}
			}
			if id != "" {
				m[id] = target
			}
		}
	}
	return m
}
