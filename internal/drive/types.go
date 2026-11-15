package drive

import (
	"path/filepath"
	"strings"
)

// typeExtensions is the search type filter: each file type and the name extensions it covers.
// "folders" is the other type. Nothing about a type is stored; it is read from the name.
var typeExtensions = map[string][]string{
	"documents":     {"doc", "docx", "docm", "dotx", "odt", "ott", "rtf", "txt", "md", "markdown"},
	"spreadsheets":  {"xls", "xlsx", "xlsm", "xltx", "ods", "ots", "csv", "tsv"},
	"presentations": {"ppt", "pptx", "pptm", "potx", "odp", "otp", "key"},
	"pdfs":          {"pdf"},
	"images":        {"png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "heic", "heif", "tif", "tiff", "avif"},
	"videos":        {"mp4", "m4v", "mov", "webm", "mkv", "avi", "ogv", "wmv", "mpg", "mpeg"},
	"audio":         {"mp3", "wav", "ogg", "oga", "opus", "m4a", "aac", "flac", "wma"},
	"archives":      {"zip", "gz", "tgz", "7z", "rar", "tar", "bz2", "xz", "zst"},
}

var extensionType = func() map[string]string {
	m := map[string]string{}
	for t, exts := range typeExtensions {
		for _, e := range exts {
			m[e] = t
		}
	}
	return m
}()

// ValidType reports whether t is a search type filter ("" is no filter).
func ValidType(t string) bool {
	_, ok := typeExtensions[t]
	return ok || t == "" || t == "folders"
}

// FileType is the search type of a file named name, by its last extension ignoring case, or "".
func FileType(name string) string {
	return extensionType[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]
}
