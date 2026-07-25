package cmd

// meta.go - Metadata Command
//
// This command reads the meta.yaml file from an encrypted archive
// and outputs its contents as JSON.
//
// Flags:
// -a, --archive   Path to the encrypted archive file (required).
// -p, --password  Password used for encrypt the archive (required)
// -k, --key       Starter key used for encryption (required).
//
// Usage Example:
// qo meta -a ./test.enc -p foo -k bar

import (
	"fmt"

	"github.com/ahmedYasserM/qo/pkg/archive"
	"github.com/spf13/cobra"
)

var metaCmd = &cobra.Command{
	Use:   "meta",
	Short: "Read metadata from an encrypted challenge archive",
	Long:  "Reads meta.yaml from an encrypted archive and outputs JSON with title, difficulty, and question.",
	RunE: func(cmd *cobra.Command, args []string) error {
		meta, err := archive.DecryptMetadata(archivePath, passwordStart)
		if err != nil {
			return err
		}

		data, err := archive.MetadataToJSON(meta)
		if err != nil {
			return err
		}

		fmt.Println(string(data))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(metaCmd)

	metaCmd.Flags().StringVarP(&archivePath, "archive", "a", "", "Path to the encrypted archive file (required)")
	metaCmd.Flags().StringVarP(&passwordStart, "password", "p", "", "Password used for encrypt the archive (required)")
	metaCmd.Flags().StringVarP(&utKeyStart, "key", "k", "", "Starter key used for decryption (required)")

	metaCmd.MarkFlagRequired("archive")
	metaCmd.MarkFlagRequired("password")
	metaCmd.MarkFlagRequired("key")
}
