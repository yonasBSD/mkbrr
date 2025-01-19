package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime/pprof"

	"github.com/autobrr/mkbrr/internal/torrent"
	"github.com/spf13/cobra"
)

var (
	// flags for create command
	trackerURL     string
	isPrivate      bool
	comment        string
	pieceLengthExp *uint // for 2^n piece length, nil means automatic
	outputPath     string
	torrentName    string
	webSeeds       []string
	noDate         bool
	source         string
	verbose        bool
)

var createCmd = &cobra.Command{
	Use:   "create <path>",
	Short: "Create a new torrent file",
	Args:  cobra.ExactArgs(1),
	RunE:  runCreate,
}

func init() {
	rootCmd.AddCommand(createCmd)

	// hide help flag
	createCmd.Flags().SortFlags = false
	createCmd.Flags().BoolP("help", "h", false, "help for create")
	createCmd.Flags().MarkHidden("help")

	createCmd.Flags().StringVarP(&trackerURL, "tracker", "t", "", "tracker URL")
	createCmd.Flags().StringArrayVarP(&webSeeds, "web-seed", "w", nil, "add web seed URLs")
	createCmd.Flags().BoolVarP(&isPrivate, "private", "p", false, "make torrent private")
	createCmd.Flags().StringVarP(&comment, "comment", "c", "", "add comment")

	// piece length flag allows setting a fixed piece size of 2^n bytes
	// if not specified, piece length is calculated automatically based on total size
	var defaultPieceLength uint
	createCmd.Flags().UintVarP(&defaultPieceLength, "piece-length", "l", 0, "set piece length to 2^n bytes (14-24, automatic if not specified)")
	createCmd.PreRun = func(cmd *cobra.Command, args []string) {
		if cmd.Flags().Changed("piece-length") {
			pieceLengthExp = &defaultPieceLength
		}
	}

	createCmd.Flags().StringVarP(&outputPath, "output", "o", "", "set output path (default: <n>.torrent)")
	createCmd.Flags().StringVarP(&torrentName, "name", "n", "", "set torrent name (default: basename of target)")
	createCmd.Flags().StringVarP(&source, "source", "s", "", "add source string")
	createCmd.Flags().BoolVarP(&noDate, "no-date", "d", false, "don't write creation date")
	createCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "be verbose")

	createCmd.Flags().String("cpuprofile", "", "write cpu profile to file")
}

func runCreate(cmd *cobra.Command, args []string) error {
	if cpuprofile, _ := cmd.Flags().GetString("cpuprofile"); cpuprofile != "" {
		f, err := os.Create(cpuprofile)
		if err != nil {
			return fmt.Errorf("could not create CPU profile: %w", err)
		}
		defer f.Close()

		if err := pprof.StartCPUProfile(f); err != nil {
			return fmt.Errorf("could not start CPU profile: %w", err)
		}
		defer pprof.StopCPUProfile()
	}

	if _, err := os.Stat(args[0]); err != nil {
		return fmt.Errorf("invalid path %q: %w", args[0], err)
	}

	if trackerURL != "" {
		if _, err := url.Parse(trackerURL); err != nil {
			return fmt.Errorf("invalid tracker URL %q: %w", trackerURL, err)
		}
	}

	for _, seed := range webSeeds {
		if _, err := url.Parse(seed); err != nil {
			return fmt.Errorf("invalid web seed URL %q: %w", seed, err)
		}
	}

	path := args[0]
	name := torrentName
	if name == "" {
		name = filepath.Base(filepath.Clean(path))
	}

	// use custom output path or default to name.torrent
	out := outputPath
	if out == "" {
		out = name + ".torrent"
	}

	opts := torrent.CreateTorrentOptions{
		Path:           path,
		Name:           name,
		TrackerURL:     trackerURL,
		WebSeeds:       webSeeds,
		IsPrivate:      isPrivate,
		Comment:        comment,
		PieceLengthExp: pieceLengthExp,
		Source:         source,
		NoDate:         noDate,
		Verbose:        verbose,
		Version:        version,
	}

	mi, err := torrent.CreateTorrent(opts)
	if err != nil {
		return err
	}

	// save the torrent file
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("error creating output file: %w", err)
	}
	defer f.Close()

	if err := mi.Write(f); err != nil {
		return fmt.Errorf("error writing torrent file: %w", err)
	}

	info := mi.GetInfo()
	fmt.Printf("\n\nCreated %s\n", out)

	// display torrent information
	torrent.NewDisplay(torrent.NewFormatter(verbose)).ShowTorrentInfo(mi, info)

	// display file tree for multi-file torrents
	if len(info.Files) > 0 {
		torrent.NewDisplay(torrent.NewFormatter(verbose)).ShowFileTree(info)
	}

	return nil
}
