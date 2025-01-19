package torrent

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/dustin/go-humanize"
)

// Formatter handles formatting of torrent information.
type Formatter struct {
	verbose bool
}

// NewFormatter creates a new Formatter with the given verbosity.
func NewFormatter(verbose bool) *Formatter {
	return &Formatter{verbose: verbose}
}

// FormatTorrentInfo returns a formatted string of torrent information.
func (f *Formatter) FormatTorrentInfo(t interface{}, info *metainfo.Info) (string, error) {
	var mi *metainfo.MetaInfo
	switch v := t.(type) {
	case *metainfo.MetaInfo:
		mi = v
	case *Torrent:
		mi = v.MetaInfo
	default:
		return "", fmt.Errorf("unsupported type: %T", t)
	}

	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("\nName: %s\n", info.Name))
	buffer.WriteString(fmt.Sprintf("Size: %s\n", humanize.Bytes(uint64(info.TotalLength()))))
	buffer.WriteString(fmt.Sprintf("Hash: %s\n", mi.HashInfoBytes().String()))

	if f.verbose {
		buffer.WriteString(fmt.Sprintf("Pieces: %d\n", len(info.Pieces)/20))
		buffer.WriteString(fmt.Sprintf("Piece Length: %s\n", humanize.Bytes(uint64(info.PieceLength))))

		private := false
		if info.Private != nil {
			private = *info.Private
		}
		buffer.WriteString(fmt.Sprintf("Private: %v\n", private))

		if mi.Comment != "" {
			buffer.WriteString(fmt.Sprintf("Comment: %s\n", mi.Comment))
		}
		if mi.Announce != "" {
			buffer.WriteString(fmt.Sprintf("\nTracker: %s\n", mi.Announce))
		}

		if mi.CreatedBy != "" {
			buffer.WriteString(fmt.Sprintf("\nCreated by: %s\n", mi.CreatedBy))
		}
		if mi.CreationDate != 0 {
			creationTime := time.Unix(mi.CreationDate, 0)
			buffer.WriteString(fmt.Sprintf("Created: %s\n", creationTime.Format(time.RFC1123)))
		}

		magnet, err := mi.MagnetV2()
		if err == nil {
			buffer.WriteString(fmt.Sprintf("\nMagnet Link: %s\n", magnet))
		}
	}

	return buffer.String(), nil
}

// FormatFileTree returns a formatted string of the torrent's file tree.
func (f *Formatter) FormatFileTree(info *metainfo.Info) (string, error) {
	if len(info.Files) == 0 {
		return "", nil
	}

	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("\nFiles  %s\n", info.Name))

	filesByPath := make(map[string][]FileEntry)
	for _, fEntry := range info.Files {
		path := strings.Join(fEntry.Path, string(filepath.Separator))
		dir := filepath.Dir(path)
		if dir == "." {
			dir = ""
		}
		filesByPath[dir] = append(filesByPath[dir], FileEntry{
			Name: filepath.Base(path),
			Size: fEntry.Length,
			Path: path,
		})
	}

	prefix := "       " // 7 spaces to align with "Files  "
	for dir, files := range filesByPath {
		if dir != "" {
			buffer.WriteString(fmt.Sprintf("%s├─%s\n", prefix, dir))
			for i, file := range files {
				var connector string
				if i == len(files)-1 {
					connector = "└─"
				} else {
					connector = "├─"
				}
				buffer.WriteString(fmt.Sprintf("%s│  %s%s [%s]\n", prefix, connector, file.Name, humanize.Bytes(uint64(file.Size))))
			}
		} else {
			for i, file := range files {
				var connector string
				if i == len(files)-1 {
					connector = "└─"
				} else {
					connector = "├─"
				}
				buffer.WriteString(fmt.Sprintf("%s%s%s [%s]\n", prefix, connector, file.Name, humanize.Bytes(uint64(file.Size))))
			}
		}
	}

	return buffer.String(), nil
}

// Display handles outputting formatted torrent information to the console.
type Display struct {
	formatter *Formatter
}

// NewDisplay creates a new Display instance.
func NewDisplay(formatter *Formatter) *Display {
	return &Display{formatter: formatter}
}

// ShowTorrentInfo prints the torrent information to the console.
func (d *Display) ShowTorrentInfo(t interface{}, info *metainfo.Info) {
	formatted, err := d.formatter.FormatTorrentInfo(t, info)
	if err != nil {
		// Handle error appropriately, possibly logging it
		return
	}
	fmt.Print(formatted)
}

// ShowFileTree prints the torrent's file tree to the console.
func (d *Display) ShowFileTree(info *metainfo.Info) {
	formatted, err := d.formatter.FormatFileTree(info)
	if err != nil {
		// Handle error appropriately, possibly logging it
		return
	}
	fmt.Print(formatted)
}
