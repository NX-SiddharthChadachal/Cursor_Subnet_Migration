// Command subnet-migrator migrates Nutanix VLAN subnets from the basic
// (Acropolis) network stack to the advanced (Flow Virtual Networking) stack.
//
// A run is three phases owned by the Controller: Prechecks decide which subnets
// are safe to move, the Executioner migrates the eligible ones in a rolling
// fashion while the Controller polls each task, and Postchecks verify the result.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/config"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/controller"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/preflight"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/report"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// Exit codes let a wrapper script tell "nothing to do" apart from "it broke".
const (
	exitOK             = 0
	exitFailure        = 1
	exitPartialSuccess = 2
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load(os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return exitFailure
	}

	log, closer, err := logging.New(logging.Options{
		Level:    cfg.LogLevel,
		Console:  os.Stderr,
		FilePath: cfg.LogFile,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start the run log: %v\n", err)
		return exitFailure
	}
	defer closer.Close()

	log.Infof("subnet-migrator %s starting", version.Tool)
	if cfg.LogFile != "" {
		log.Infof("Run log is also being written to %s", cfg.LogFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pre, err := preflight.Run(ctx, cfg, log)
	if err != nil {
		log.Errorf("Preflight checks failed: %v", err)
		return exitFailure
	}

	ctrl := controller.New(cfg, log, pre, os.Stdin, os.Stdout)
	runReport, runErr := ctrl.Run(ctx)

	report.WriteText(os.Stdout, runReport)
	if cfg.ReportPath != "" {
		if err := report.WriteJSON(cfg.ReportPath, runReport); err != nil {
			log.Errorf("Could not write the JSON report: %v", err)
		} else {
			log.Infof("JSON report written to %s", cfg.ReportPath)
		}
	}

	switch {
	case runErr != nil:
		log.Errorf("Run finished with errors: %v", runErr)
		if runReport.Execution != nil && len(runReport.Execution.Migrated()) > 0 {
			return exitPartialSuccess
		}
		return exitFailure
	case runReport.Postchecks != nil && runReport.Postchecks.HasBlockers():
		log.Errorf("Run finished but the postchecks flagged conditions that need a Nutanix support ticket")
		return exitPartialSuccess
	default:
		log.Infof("Run finished")
		return exitOK
	}
}
