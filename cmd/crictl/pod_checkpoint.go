/*
Copyright 2026 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	pb "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var checkpointPodCommand = &cli.Command{
	Name:                   "checkpointpod",
	Usage:                  "Checkpoint a running pod sandbox",
	ArgsUsage:              "POD-ID",
	UseShortOptionHandling: true,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "path",
			Aliases: []string{"p"},
			Usage:   "Path to the directory where the checkpoint will be stored",
		},
		&cli.Int64Flag{
			Name:    "timeout",
			Aliases: []string{"t"},
			Usage:   "Timeout in seconds for the checkpoint operation (0 means runtime default)",
			Value:   0,
		},
	},
	Action: func(c *cli.Context) error {
		if c.NArg() == 0 {
			return errors.New("POD-ID cannot be empty")
		}
		if c.NArg() > 1 {
			return errors.New("only one pod sandbox can be checkpointed at a time")
		}

		path := c.String("path")
		if path == "" {
			return errors.New(
				"cannot checkpoint a pod without specifying the checkpoint path. " +
					"Use --path=/path/to/checkpoint/dir",
			)
		}

		runtimeClient, err := getRuntimeService(c, 0)
		if err != nil {
			return err
		}

		podID := c.Args().First()
		request := &pb.CheckpointPodRequest{
			PodSandboxId: podID,
			Path:         path,
			Timeout:      c.Int64("timeout"),
		}
		logrus.Debugf("CheckpointPodRequest: %v", request)

		_, err = InterruptableRPC(c.Context, func(ctx context.Context) (*pb.CheckpointPodResponse, error) {
			return nil, runtimeClient.CheckpointPod(ctx, request)
		})
		if err != nil {
			return fmt.Errorf("checkpointing pod %q failed: %w", podID, err)
		}

		fmt.Printf("Pod %s checkpointed to %s\n", podID, path)
		return nil
	},
}

var restorePodCommand = &cli.Command{
	Name:                   "restorepod",
	Usage:                  "Restore a pod sandbox from a checkpoint",
	ArgsUsage:              "CHECKPOINT-PATH",
	UseShortOptionHandling: true,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Usage:   "Path to a PodSandboxConfig JSON file to override the checkpointed config",
		},
	},
	Action: func(c *cli.Context) error {
		if c.NArg() == 0 {
			return errors.New("CHECKPOINT-PATH cannot be empty")
		}
		if c.NArg() > 1 {
			return errors.New("only one checkpoint path can be restored at a time")
		}

		runtimeClient, err := getRuntimeService(c, 0)
		if err != nil {
			return err
		}

		checkpointPath := c.Args().First()
		request := &pb.RestorePodRequest{
			Path: checkpointPath,
		}

		// Optionally load a sandbox config override.
		if configPath := c.String("config"); configPath != "" {
			config, err := loadPodSandboxConfig(configPath)
			if err != nil {
				return fmt.Errorf("failed to load pod sandbox config: %w", err)
			}
			request.Config = config
		}

		logrus.Debugf("RestorePodRequest: %v", request)

		podID, err := InterruptableRPC(c.Context, func(ctx context.Context) (string, error) {
			return runtimeClient.RestorePod(ctx, request)
		})
		if err != nil {
			return fmt.Errorf("restoring pod from %q failed: %w", checkpointPath, err)
		}

		fmt.Printf("Pod restored: %s\n", podID)
		return nil
	},
}
