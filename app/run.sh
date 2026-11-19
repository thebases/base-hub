#!/bin/bash

# Run the Go application with development config
go run ./main.go start --force-config -c internal/config/config_dev.yaml
