package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var (
	// flags for inspect command
	showMagnet bool
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <torrent-file>",
	Short: "Inspect a torrent file",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func init() {
	rootCmd.AddCommand(inspectCmd)

	// hide help flag
	inspectCmd.Flags().SortFlags = false
	inspectCmd.Flags().BoolP("help", "h", false, "help for inspect")
	inspectCmd.Flags().MarkHidden("help")

	// add flags to inspect command
	// inspectCmd.Flags().BoolVarP(&showMagnet, "magnet", "M", false, "show magnet link (forces display for private torrents)")
}

func runInspect(cmd *cobra.Command, args []string) error {
	mi, err := metainfo.LoadFromFile(args[0])
	if err != nil {
		return fmt.Errorf("error loading torrent: %w", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("error parsing info: %w", err)
	}

	// calculate total size
	var totalSize int64
	if !info.IsDir() {
		totalSize = info.Length
	} else {
		for _, file := range info.Files {
			totalSize += file.Length
		}
	}

	// handle nil pointer for Private field
	private := false
	if info.Private != nil {
		private = *info.Private
	}

	// display basic information
	fmt.Printf("\nName: %s\n", info.Name)
	fmt.Printf("Size: %s\n", humanize.Bytes(uint64(totalSize)))
	fmt.Printf("Pieces: %d\n", len(info.Pieces)/20)
	fmt.Printf("Piece Length: %s\n", humanize.Bytes(uint64(info.PieceLength)))
	fmt.Printf("Private: %v\n", private)
	fmt.Printf("\n") // spacing between groups

	// display technical information
	fmt.Printf("Hash: %s\n", mi.HashInfoBytes().String())
	fmt.Printf("Tracker: %s\n", mi.Announce)

	// display tracker list if present
	if len(mi.AnnounceList) > 0 {
		fmt.Println("\nTrackers:")
		for i, tier := range mi.AnnounceList {
			fmt.Printf("Tier %d:\n", i+1)
			for _, tracker := range tier {
				fmt.Printf("  - %s\n", tracker)
			}
		}
	}

	// display creation info
	if mi.CreatedBy != "" || mi.CreationDate != 0 || mi.Comment != "" {
		fmt.Println() // spacing before creation info
		if mi.CreatedBy != "" {
			fmt.Printf("Created by: %s\n", mi.CreatedBy)
		}
		if mi.CreationDate != 0 {
			creationTime := time.Unix(mi.CreationDate, 0)
			fmt.Printf("Created: %s\n", creationTime.Format(time.RFC1123))
		}
		if mi.Comment != "" {
			fmt.Printf("Comment: %s\n", mi.Comment)
		}
	}

	// display magnet link
	magnet, _ := mi.MagnetV2()
	fmt.Printf("\nMagnet Link: %s\n", magnet)

	// display file information for multi-file torrents
	if info.IsDir() {
		fmt.Printf("\nFiles  %s\n", info.Name)

		// organize files by path components
		filesByPath := make(map[string][]FileEntry)
		for _, file := range info.Files {
			path := file.DisplayPath(&info)
			dir := filepath.Dir(path)
			if dir == "." {
				dir = ""
			}
			filesByPath[dir] = append(filesByPath[dir], FileEntry{
				name: filepath.Base(path),
				size: file.Length,
				path: path,
			})
		}

		// print files in tree structure
		prefix := "       " // 7 spaces to align with "Files  "
		for dir, files := range filesByPath {
			if dir != "" {
				fmt.Printf("%s├─%s\n", prefix, dir)
				for i, file := range files {
					if i == len(files)-1 {
						fmt.Printf("%s│  └─%s [%s]\n", prefix, file.name, humanize.Bytes(uint64(file.size)))
					} else {
						fmt.Printf("%s│  ├─%s [%s]\n", prefix, file.name, humanize.Bytes(uint64(file.size)))
					}
				}
			} else {
				for i, file := range files {
					if i == len(files)-1 {
						fmt.Printf("%s└─%s [%s]\n", prefix, file.name, humanize.Bytes(uint64(file.size)))
					} else {
						fmt.Printf("%s├─%s [%s]\n", prefix, file.name, humanize.Bytes(uint64(file.size)))
					}
				}
			}
		}
	}

	return nil
}

type FileEntry struct {
	name string
	size int64
	path string
}
