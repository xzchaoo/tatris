// Copyright 2022 Tatris Project Authors. Licensed under Apache-2.0.

// Package config organizes codes of the bluge config
package config

import (
	"path"

	"github.com/tatris-io/tatris/internal/indexlib/bluge/directory/fs"

	"github.com/tatris-io/tatris/internal/indexlib/bluge/directory/oss"

	"github.com/blugelabs/bluge"
	"github.com/blugelabs/bluge/index"
)

func GetFSConfig(filepath string, filename string) bluge.Config {
	return bluge.DefaultConfigWithDirectory(func() index.Directory {
		return fs.NewFsDirectory(path.Join(filepath, filename))
	})
}

func GetOSSConfig(endpoint, bucket, accessKeyID, secretAccessKey, filename string) bluge.Config {
	return bluge.DefaultConfigWithDirectory(func() index.Directory {
		return oss.NewOssDirectory(endpoint, bucket, accessKeyID, secretAccessKey, filename)
	})
}
