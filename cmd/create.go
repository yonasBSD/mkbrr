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
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/dustin/go-humanize"
	"github.com/schollz/progressbar/v3"
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

	// file patterns to ignore (case insensitive)
	ignoredPatterns = []string{
		".torrent",    // torrent files
		".ds_store",   // macOS system files
		"thumbs.db",   // Windows thumbnail cache
		"desktop.ini", // Windows folder settings
	}
)

// shouldIgnoreFile checks if a file should be ignored based on predefined patterns
func shouldIgnoreFile(path string) bool {
	lowerPath := strings.ToLower(path)
	for _, pattern := range ignoredPatterns {
		if strings.HasSuffix(lowerPath, pattern) {
			return true
		}
	}
	return false
}

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
	pieces     [][]byte
	pieceLen   int64
	numPieces  int
	files      []fileEntry
	progress   *progressbar.ProgressBar
	bufferPool *sync.Pool
	readSize   int
}

// helper function to set read buffer size
func setReadBuffer(f *os.File, size int) error {
	return syscall.SetsockoptInt(int(f.Fd()), syscall.SOL_SOCKET, syscall.SO_RCVBUF, size)
}

type fileEntry struct {
	path   string
	length int64
	offset int64
}

// maintain open files with current positions
type fileReader struct {
	file     *os.File
	position int64
	length   int64
}

// optimize buffer and worker count based on workload characteristics
func (h *pieceHasher) optimizeForWorkload() (int, int) {
	var totalSize int64
	maxFileSize := int64(0)
	for _, f := range h.files {
		totalSize += f.length
		if f.length > maxFileSize {
			maxFileSize = f.length
		}
	}
	avgFileSize := totalSize / int64(len(h.files))

	// determine optimal read size and worker count
	var readSize, numWorkers int

	switch {
	case len(h.files) == 1:
		// single file optimization
		if totalSize < 1<<20 { // < 1MB
			readSize = 64 << 10 // 64KB
			numWorkers = 1
		} else if totalSize < 1<<30 { // < 1GB
			readSize = 1 << 20 // 1MB
			numWorkers = 2
		} else {
			readSize = 4 << 20 // 4MB
			numWorkers = 4
		}
	case avgFileSize < 1<<20: // small files (< 1MB avg)
		readSize = 256 << 10 // 256KB
		numWorkers = min(8, runtime.NumCPU())
	case avgFileSize < 10<<20: // medium files (< 10MB avg)
		readSize = 1 << 20 // 1MB
		numWorkers = min(4, runtime.NumCPU())
	default: // large files
		readSize = 4 << 20 // 4MB
		numWorkers = min(2, runtime.NumCPU())
	}

	// adjust workers based on piece count
	if numWorkers > h.numPieces {
		numWorkers = h.numPieces
	}

	return readSize, numWorkers
}

// hashPieces reads and hashes file pieces in parallel
func (h *pieceHasher) hashPieces(numWorkers int) error {
	// optimize read size and worker count
	h.readSize, numWorkers = h.optimizeForWorkload()

	// initialize buffer pool with optimized buffer size
	h.bufferPool = &sync.Pool{
		New: func() interface{} {
			return make([]byte, h.readSize)
		},
	}

	// use atomic counter for progress
	var completedPieces uint64

	// optimize number of workers
	if numWorkers > h.numPieces {
		numWorkers = h.numPieces
	}

	// calculate pieces per worker
	piecesPerWorker := (h.numPieces + numWorkers - 1) / numWorkers

	// create error channel
	errors := make(chan error, numWorkers)

	// create progress bar
	h.progress = progressbar.NewOptions(h.numPieces,
		progressbar.OptionSetDescription("Hashing pieces"),
		progressbar.OptionSetItsString("piece"),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
	)

	// start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		start := i * piecesPerWorker
		end := start + piecesPerWorker
		if end > h.numPieces {
			end = h.numPieces
		}

		wg.Add(1)
		go func(startPiece, endPiece int) {
			defer wg.Done()
			if err := h.hashPieceRange(startPiece, endPiece, &completedPieces); err != nil {
				errors <- err
			}
		}(start, end)
	}

	// start progress updater with reduced update frequency
	go func() {
		for {
			completed := atomic.LoadUint64(&completedPieces)
			if completed >= uint64(h.numPieces) {
				break
			}
			h.progress.Set(int(completed))
			time.Sleep(200 * time.Millisecond) // reduced update frequency
		}
	}()

	// wait for completion
	wg.Wait()
	close(errors)

	// check for errors
	for err := range errors {
		if err != nil {
			return err
		}
	}

	h.progress.Finish()
	return nil
}

// hashPieceRange optimized for better sequential reads
func (h *pieceHasher) hashPieceRange(startPiece, endPiece int, completedPieces *uint64) error {
	// get buffer from pool
	buf := h.bufferPool.Get().([]byte)
	defer h.bufferPool.Put(buf)

	// pre-allocate hash buffer and reuse it
	hasher := sha1.New()

	// maintain file readers with current positions
	readers := make(map[string]*fileReader)
	defer func() {
		for _, r := range readers {
			r.file.Close()
		}
	}()

	for pieceIndex := startPiece; pieceIndex < endPiece; pieceIndex++ {
		pieceOffset := int64(pieceIndex) * h.pieceLen
		pieceLength := h.pieceLen

		// adjust length for last piece
		if pieceIndex == h.numPieces-1 {
			var totalLength int64
			for _, f := range h.files {
				totalLength += f.length
			}
			remaining := totalLength - pieceOffset
			if remaining < pieceLength {
				pieceLength = remaining
			}
		}

		hasher.Reset()
		remainingPiece := pieceLength

		// read piece data from files
		for _, file := range h.files {
			// skip files before piece offset
			if pieceOffset >= file.offset+file.length {
				continue
			}
			// stop if we've read the full piece
			if remainingPiece <= 0 {
				break
			}

			// calculate file read position
			readStart := pieceOffset - file.offset
			if readStart < 0 {
				readStart = 0
			}

			// calculate how much to read from this file
			readLength := file.length - readStart
			if readLength > remainingPiece {
				readLength = remainingPiece
			}

			// get or create file reader
			reader, ok := readers[file.path]
			if !ok {
				f, err := os.OpenFile(file.path, os.O_RDONLY, 0)
				if err != nil {
					return fmt.Errorf("failed to open file %s: %w", file.path, err)
				}
				reader = &fileReader{
					file:     f,
					position: 0,
					length:   file.length,
				}
				readers[file.path] = reader
			}

			// seek to correct position if needed
			if reader.position != readStart {
				if _, err := reader.file.Seek(readStart, 0); err != nil {
					return fmt.Errorf("failed to seek in file %s: %w", file.path, err)
				}
				reader.position = readStart
			}

			// read file data in chunks
			remaining := readLength
			for remaining > 0 {
				n := int(remaining)
				if n > len(buf) {
					n = len(buf)
				}

				read, err := io.ReadFull(reader.file, buf[:n])
				if err != nil && err != io.ErrUnexpectedEOF {
					return fmt.Errorf("failed to read file %s: %w", file.path, err)
				}

				hasher.Write(buf[:read])
				remaining -= int64(read)
				remainingPiece -= int64(read)
				reader.position += int64(read)
				pieceOffset += int64(read)
			}
		}

		// store piece hash
		h.pieces[pieceIndex] = hasher.Sum(nil)
		atomic.AddUint64(completedPieces, 1)
	}

	return nil
}

// buildInfoDict builds the info dictionary with parallel piece hashing
func buildInfoDict(path string, name string, pieceLength int64) (*metainfo.Info, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	// Initialize info with dynamic piece length instead of hardcoded value
	info := &metainfo.Info{
		PieceLength: pieceLength, // Remove hardcoded 262144 value
	}

	if !fileInfo.IsDir() {
		// Single file mode
		info.Name = filepath.Base(path)
		info.Length = fileInfo.Size()
		if isPrivate {
			private := true
			info.Private = &private
		}

		// Calculate pieces for single file
		numPieces := (fileInfo.Size() + pieceLength - 1) / pieceLength
		hasher := &pieceHasher{
			pieces:    make([][]byte, numPieces),
			pieceLen:  pieceLength,
			numPieces: int(numPieces),
			files: []fileEntry{{
				path:   path,
				length: fileInfo.Size(),
				offset: 0,
			}},
		}

		if err := hasher.hashPieces(runtime.NumCPU()); err != nil {
			return nil, err
		}

		// Set pieces directly from hasher
		info.Pieces = make([]byte, len(hasher.pieces)*20)
		for i, piece := range hasher.pieces {
			copy(info.Pieces[i*20:], piece)
		}

		return info, nil
	}

	// collect file information in a single pass
	var files []fileEntry
	var totalLength int64
	var baseDir string

	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if baseDir == "" {
				baseDir = filePath
			}
			return nil
		}
		if shouldIgnoreFile(filePath) {
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

	// Sort files by path to ensure consistent ordering
	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})

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
	info = &metainfo.Info{
		Name:        name,
		PieceLength: pieceLength,
	}

	// add files
	if len(files) == 1 {
		info.Length = files[0].length
	} else {
		info.Files = make([]metainfo.FileInfo, len(files))
		for i, f := range files {
			relPath, _ := filepath.Rel(baseDir, f.path)
			// Use forward slashes for path components regardless of OS
			pathComponents := strings.Split(filepath.ToSlash(relPath), "/")
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

	// add new flag for CPU profiling
	createCmd.Flags().String("cpuprofile", "", "write cpu profile to file")
}

func runCreate(cmd *cobra.Command, args []string) error {
	// Start CPU profiling if requested
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

	// Normalize the input path to use forward slashes
	path = filepath.ToSlash(path)

	// Use clean paths for name derivation
	var name string
	if torrentName == "" {
		name = filepath.Base(filepath.Clean(path))
	} else {
		name = torrentName
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
		if shouldIgnoreFile(filePath) {
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

	// Sort files to ensure consistent ordering
	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})

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

	fmt.Printf("\nName: %s\n", info.Name)
	fmt.Printf("Size: %s\n", humanize.Bytes(uint64(totalSize)))
	fmt.Printf("Pieces: %d\n", len(info.Pieces)/20)
	fmt.Printf("Piece Length: %s\n", humanize.Bytes(uint64(info.PieceLength)))
	fmt.Printf("Private: %v\n", isPrivate)
	fmt.Printf("\n")

	fmt.Printf("Hash: %s\n", mi.HashInfoBytes().String())
	fmt.Printf("Tracker: %s\n", trackerURL)

	if len(webSeeds) > 0 {
		fmt.Println("\nWeb Seeds:")
		for _, seed := range webSeeds {
			fmt.Printf("  - %s\n", seed)
		}
	}

	// display creation info
	fmt.Println()
	fmt.Printf("Created by: %s\n", mi.CreatedBy)
	if !noDate {
		creationTime := time.Unix(mi.CreationDate, 0)
		fmt.Printf("Created: %s\n", creationTime.Format(time.RFC1123))
	}
	if comment != "" {
		fmt.Printf("Comment: %s\n", comment)
	}

	// generate and display magnet link
	magnet, _ := mi.MagnetV2()
	fmt.Printf("\nMagnet Link: %s\n", magnet)

	// display file information for multi-file torrents
	if len(files) > 1 {
		fmt.Printf("\nFiles  %s\n", info.Name)

		// organize files by path components
		filesByPath := make(map[string][]FileEntry)
		for _, f := range files {
			relPath, _ := filepath.Rel(baseDir, f.path)
			dir := filepath.Dir(relPath)
			if dir == "." {
				dir = ""
			}
			filesByPath[dir] = append(filesByPath[dir], FileEntry{
				name: filepath.Base(relPath),
				size: f.length,
				path: relPath,
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
