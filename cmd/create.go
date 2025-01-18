package cmd

import (
	"crypto/sha1"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	// use constants instead of magic numbers
	minLength := int64(1) << 14
	maxLength := int64(1) << 24

	// find the required exponent for the bounded length
	boundedLength := min(max(length, minLength), maxLength)
	exp := uint(math.Log2(float64(boundedLength)))

	if verbose {
		fmt.Printf("Total size: %d bytes\n", totalSize)
		fmt.Printf("Calculated length: %d bytes (2^%d)\n", boundedLength, exp)
	}

	return exp
}

// pieceHasher handles parallel hashing of pieces
type pieceHasher struct {
	pieces    [][]byte
	pieceLen  int64
	numPieces int
	files     []fileEntry
}

type fileEntry struct {
	path   string
	length int64
	offset int64
}

// hashPieces reads and hashes file pieces in parallel
func (h *pieceHasher) hashPieces(numWorkers int) error {
	// calculate pieces per worker
	piecesPerWorker := (h.numPieces + numWorkers - 1) / numWorkers

	// create error channel for workers
	errors := make(chan error, numWorkers)

	// start workers
	for i := 0; i < numWorkers; i++ {
		start := i * piecesPerWorker
		end := start + piecesPerWorker
		if end > h.numPieces {
			end = h.numPieces
		}

		go func(startPiece, endPiece int) {
			if err := h.hashPieceRange(startPiece, endPiece); err != nil {
				errors <- err
			} else {
				errors <- nil
			}
		}(start, end)
	}

	// wait for all workers
	for i := 0; i < numWorkers; i++ {
		if err := <-errors; err != nil {
			return err
		}
	}

	return nil
}

// hashPieceRange hashes a range of pieces with efficient buffering
func (h *pieceHasher) hashPieceRange(startPiece, endPiece int) error {
	// use a larger buffer for reading
	const bufferSize = 1 << 20 // 1MB buffer
	buf := make([]byte, bufferSize)
	// pre-allocate hash buffer with exact size
	hashBuf := make([]byte, 20)

	// maintain open files
	openFiles := make(map[string]*os.File)
	defer func() {
		for _, f := range openFiles {
			f.Close()
		}
	}()

	// process each piece in the range
	for pieceIndex := startPiece; pieceIndex < endPiece; pieceIndex++ {
		offset := int64(pieceIndex) * h.pieceLen
		hasher := sha1.New()
		remainingPiece := h.pieceLen

		// adjust for last piece
		if pieceIndex == h.numPieces-1 {
			var totalLength int64
			for _, f := range h.files {
				totalLength += f.length
			}
			remainingPiece = totalLength - offset
		}

		// read from files that contain this piece
		for _, file := range h.files {
			// skip files before the piece
			if offset >= file.offset+file.length {
				continue
			}

			// calculate file offsets
			fileOffset := int64(0)
			if offset > file.offset {
				fileOffset = offset - file.offset
			}

			// calculate how much to read
			toRead := file.length - fileOffset
			if toRead > remainingPiece {
				toRead = remainingPiece
			}

			if toRead <= 0 {
				break
			}

			// get or open file
			f, ok := openFiles[file.path]
			if !ok {
				var err error
				f, err = os.Open(file.path)
				if err != nil {
					return err
				}
				openFiles[file.path] = f
			}

			// seek to correct position
			if _, err := f.Seek(fileOffset, 0); err != nil {
				return err
			}

			// read file data in chunks
			for toRead > 0 {
				n := toRead
				if n > bufferSize {
					n = bufferSize
				}

				read, err := io.ReadFull(f, buf[:n])
				if err != nil && err != io.ErrUnexpectedEOF {
					return err
				}

				hasher.Write(buf[:read])
				toRead -= int64(read)
				remainingPiece -= int64(read)
			}

			if remainingPiece <= 0 {
				break
			}
		}

		// reuse hash buffer instead of allocating new one
		h.pieces[pieceIndex] = hasher.Sum(hashBuf[:0])
	}

	return nil
}

// buildInfoDict builds the info dictionary with parallel piece hashing
func buildInfoDict(path string, name string, pieceLength int64) (*metainfo.Info, error) {
	// collect file information in a single pass
	var files []fileEntry
	var totalLength int64
	var baseDir string

	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if baseDir == "" {
				baseDir = filePath
			}
			return nil
		}

		files = append(files, fileEntry{
			path:   filePath,
			length: info.Size(),
			offset: totalLength,
		})
		totalLength += info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}

	// calculate number of pieces
	numPieces := (totalLength + pieceLength - 1) / pieceLength

	// initialize piece hasher
	hasher := &pieceHasher{
		pieces:    make([][]byte, numPieces),
		pieceLen:  pieceLength,
		numPieces: int(numPieces),
		files:     files,
	}

	// determine optimal number of workers based on CPU cores
	numWorkers := runtime.NumCPU()
	if err := hasher.hashPieces(numWorkers); err != nil {
		return nil, err
	}

	// build info dictionary
	info := &metainfo.Info{
		Name:        name,
		PieceLength: pieceLength,
	}

	// concatenate piece hashes
	info.Pieces = make([]byte, 0, len(hasher.pieces)*20)
	for _, piece := range hasher.pieces {
		info.Pieces = append(info.Pieces, piece...)
	}

	// add files
	if len(files) == 1 {
		info.Length = files[0].length
	} else {
		info.Files = make([]metainfo.FileInfo, len(files))
		for i, f := range files {
			relPath, _ := filepath.Rel(baseDir, f.path)
			pathComponents := strings.Split(relPath, string(filepath.Separator))
			info.Files[i] = metainfo.FileInfo{
				Path:   pathComponents,
				Length: f.length,
			}
		}
	}

	return info, nil
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
	// validate path exists before proceeding
	if _, err := os.Stat(args[0]); err != nil {
		return fmt.Errorf("invalid path %q: %w", args[0], err)
	}

	// validate tracker URL if provided
	if trackerURL != "" {
		if _, err := url.Parse(trackerURL); err != nil {
			return fmt.Errorf("invalid tracker URL %q: %w", trackerURL, err)
		}
	}

	// validate web seed URLs
	for _, seed := range webSeeds {
		if _, err := url.Parse(seed); err != nil {
			return fmt.Errorf("invalid web seed URL %q: %w", seed, err)
		}
	}

	//startTime := time.Now()
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

	// collect file information and calculate total size in a single pass
	files := make([]fileEntry, 0, 1) // most torrents are single file
	var totalSize int64
	var baseDir string

	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if baseDir == "" {
				baseDir = filePath
			}
			return nil
		}
		files = append(files, fileEntry{
			path:   filePath,
			length: info.Size(),
			offset: totalSize,
		})
		totalSize += info.Size()
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking path: %w", err)
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

	// calculate number of pieces
	pieceLenInt := int64(1) << pieceLength
	numPieces := (totalSize + pieceLenInt - 1) / pieceLenInt

	// initialize piece hasher with pre-allocated slices
	hasher := &pieceHasher{
		pieces:    make([][]byte, numPieces), // each piece will store a 20-byte SHA1 hash
		pieceLen:  pieceLenInt,
		numPieces: int(numPieces),
		files:     files,
	}

	// use 4 workers or number of CPUs, whichever is less
	numWorkers := runtime.NumCPU()
	if numWorkers > 4 {
		numWorkers = 4
	}
	if err := hasher.hashPieces(numWorkers); err != nil {
		return fmt.Errorf("error hashing pieces: %w", err)
	}

	// build info dictionary
	info := &metainfo.Info{
		Name:        name,
		PieceLength: pieceLenInt,
		Private:     &isPrivate,
	}

	if source != "" {
		info.Source = source
	}

	// pre-allocate and concatenate piece hashes
	info.Pieces = make([]byte, len(hasher.pieces)*20)
	for i, piece := range hasher.pieces {
		copy(info.Pieces[i*20:], piece)
	}

	// add files
	if len(files) == 1 {
		info.Length = files[0].length
	} else {
		info.Files = make([]metainfo.FileInfo, len(files))
		for i, f := range files {
			relPath, _ := filepath.Rel(baseDir, f.path)
			pathComponents := strings.Split(relPath, string(filepath.Separator))
			info.Files[i] = metainfo.FileInfo{
				Path:   pathComponents,
				Length: f.length,
			}
		}
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
	//elapsed := time.Since(startTime)
	//fmt.Printf("Duration: %s\n", elapsed.Round(time.Millisecond))

	return nil
}
