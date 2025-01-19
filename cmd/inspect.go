package cmd

import (
	"fmt"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/autobrr/mkbrr/internal/torrent"
	"github.com/spf13/cobra"
)

var (
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
	inspectCmd.Flags().SortFlags = false
	inspectCmd.Flags().BoolP("help", "h", false, "help for inspect")
	inspectCmd.Flags().MarkHidden("help")
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

	torrent.NewDisplay(torrent.NewFormatter(true)).ShowTorrentInfo(mi, &info)

	if info.IsDir() {
		torrent.NewDisplay(torrent.NewFormatter(true)).ShowFileTree(&info)
	}

	return nil
}
