#!/bin/bash

# Run the Go application with development config
go run ./main.go init-db --config internal/config/config_dev.yaml $1
