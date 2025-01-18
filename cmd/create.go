package cmd

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/dustin/go-humanize"
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

// calculatePieceLength calculates the optimal piece length based on total size
// using the formula: 2^(log2(size)/2 + 4)
// minimum: 16 KiB (2^14), maximum: 16 MiB (2^24)
// This provides a good balance between:
// - Small enough pieces for quick verification and upload capability
// - Large enough pieces to keep protocol overhead and metadata size reasonable
// - Reasonable piece counts for different file sizes
// Source: https://imdl.io/book/bittorrent/piece-length-selection.html
func calculatePieceLength(totalSize int64) uint {
	// calculate exponent using log2 of content length
	exponent := math.Log2(float64(totalSize))

	// use their formula: 2^(log2(size)/2 + 4)
	length := int64(1) << uint(exponent/2+4)

	// enforce min (16 KiB) and max (16 MiB) bounds
	minLength := int64(16 * 1024)        // 16 KiB
	maxLength := int64(16 * 1024 * 1024) // 16 MiB

	// find the required exponent for the bounded length
	boundedLength := min(max(length, minLength), maxLength)
	exp := uint(math.Log2(float64(boundedLength)))

	if verbose {
		fmt.Printf("Total size: %d bytes\n", totalSize)
		fmt.Printf("Calculated length: %d bytes (2^%d)\n", boundedLength, exp)
	}

	return exp
}

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

	// add flags to create command
	createCmd.Flags().StringVarP(&trackerURL, "tracker", "t", "", "tracker URL")
	createCmd.Flags().StringArrayVarP(&webSeeds, "web-seed", "w", nil, "add web seed URLs")
	createCmd.Flags().BoolVarP(&isPrivate, "private", "p", false, "make torrent private")
	createCmd.Flags().StringVarP(&comment, "comment", "c", "", "add comment")

	// piece length is now a pointer to allow nil (automatic) value
	var defaultPieceLength uint
	createCmd.Flags().UintVarP(&defaultPieceLength, "piece-length", "l", 0, "set piece length to 2^n bytes (14-24, automatic if not specified)")
	if defaultPieceLength != 0 {
		pieceLengthExp = &defaultPieceLength
	}

	createCmd.Flags().StringVarP(&outputPath, "output", "o", "", "set output path (default: <n>.torrent)")
	createCmd.Flags().StringVarP(&torrentName, "name", "n", "", "set torrent name (default: basename of target)")
	createCmd.Flags().StringVarP(&source, "source", "s", "", "add source string")
	createCmd.Flags().BoolVarP(&noDate, "no-date", "d", false, "don't write creation date")
	createCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "be verbose")
}

func runCreate(cmd *cobra.Command, args []string) error {
	startTime := time.Now()
	path := args[0]

	// use custom name or default to basename
	name := torrentName
	if name == "" {
		name = filepath.Base(path)
	}

	// use custom output path or default to name.torrent
	out := outputPath
	if out == "" {
		out = name + ".torrent"
	}

	// create a new metainfo builder
	mi := &metainfo.MetaInfo{
		CreatedBy: "mkbrr",
		Announce:  trackerURL,
		Comment:   comment,
	}

	// only set creation date if not disabled
	if !noDate {
		mi.CreationDate = time.Now().Unix()
	}

	// calculate total size for automatic piece length
	var totalSize int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error calculating total size: %w", err)
	}

	// determine piece length
	var pieceLength uint
	if pieceLengthExp == nil {
		pieceLength = calculatePieceLength(totalSize)
	} else {
		if *pieceLengthExp < 14 || *pieceLengthExp > 24 {
			return fmt.Errorf("piece length exponent must be between 14 (16 KiB) and 24 (16 MiB)")
		}
		pieceLength = *pieceLengthExp
	}

	// build the info dictionary
	info := metainfo.Info{
		Name:        name,
		PieceLength: 1 << pieceLength,
		Private:     &isPrivate,
	}

	// add source if specified
	if source != "" {
		info.Source = source
	}

	// add files to the info
	if err := info.BuildFromFilePath(path); err != nil {
		return fmt.Errorf("error adding file/directory: %w", err)
	}

	// encode the info dictionary
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return fmt.Errorf("error encoding info: %w", err)
	}
	mi.InfoBytes = infoBytes

	// add web seeds if specified
	if len(webSeeds) > 0 {
		mi.UrlList = webSeeds
	}

	// verbose output if enabled
	if verbose {
		fmt.Printf("Total size: %s\n", humanize.Bytes(uint64(totalSize)))
		fmt.Printf("Piece length: %s (2^%d)\n", humanize.Bytes(uint64(info.PieceLength)), pieceLength)
		fmt.Printf("Number of pieces: %d\n", len(info.Pieces)/20)
		if len(webSeeds) > 0 {
			fmt.Printf("Web seeds: %v\n", webSeeds)
		}
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

	fmt.Printf("Created torrent: %s\n", out)
	fmt.Printf("Info Hash: %s\n", mi.HashInfoBytes().String())

	// generate and display magnet link
	magnet, _ := mi.MagnetV2()
	fmt.Printf("Magnet Link: %s\n", magnet)

	// print elapsed time
	elapsed := time.Since(startTime)
	fmt.Printf("Duration: %s\n", elapsed.Round(time.Millisecond))

	return nil
}
