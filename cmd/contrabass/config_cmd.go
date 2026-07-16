package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/junhoyeo/contrabass/internal/config"
)

func newConfigCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and inspect workflow configuration",
	}
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "path to WORKFLOW.md file (required)")
	_ = cmd.MarkPersistentFlagRequired("config")

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate WORKFLOW.md without starting Contrabass",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := config.ParseWorkflow(configPath); err != nil {
				return fmt.Errorf("validating workflow config: %w", err)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "configuration valid")
			return err
		},
	}

	var outputFormat string
	effectiveCmd := &cobra.Command{
		Use:   "effective",
		Short: "Print resolved values, sources, and reload policies",
		Args:  cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			outputFormat = strings.ToLower(strings.TrimSpace(outputFormat))
			switch outputFormat {
			case "json", "yaml", "yml", "":
				return nil
			default:
				return fmt.Errorf("unsupported format %q (valid values: yaml, json)", outputFormat)
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.ParseWorkflow(configPath)
			if err != nil {
				return fmt.Errorf("parsing workflow config: %w", err)
			}
			effective, err := config.BuildEffectiveConfig(cfg)
			if err != nil {
				return fmt.Errorf("resolving workflow config: %w", err)
			}

			switch outputFormat {
			case "json":
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(effective); err != nil {
					return fmt.Errorf("encoding effective config as JSON: %w", err)
				}
				return nil
			case "yaml", "yml", "":
				output, err := yaml.Marshal(effective)
				if err != nil {
					return fmt.Errorf("encoding effective config as YAML: %w", err)
				}
				if _, err := cmd.OutOrStdout().Write(output); err != nil {
					return fmt.Errorf("writing effective config: %w", err)
				}
				return nil
			}
			return nil
		},
	}
	effectiveCmd.Flags().StringVar(&outputFormat, "format", "yaml", "output format (yaml or json)")

	cmd.AddCommand(validateCmd, effectiveCmd)
	return cmd
}
