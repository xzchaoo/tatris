// Copyright 2023 Tatris Project Authors. Licensed under Apache-2.0.

// Package oss is used to implement the AliCloud-Object-Storage-Service storage medium for the
// underlying data and indexes.
package oss

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"

	"github.com/tatris-io/tatris/internal/common/utils"

	"github.com/tatris-io/tatris/internal/common/log/logger"
	"go.uber.org/zap"

	"github.com/blugelabs/bluge/index"
	segment "github.com/blugelabs/bluge_segment_api"
)

type OssDirectory struct {
	client *oss.Client
	bucket string
	index  string
	lock   sync.RWMutex
}

func NewOssDirectory(endpoint, bucket, accessKeyID, secretAccessKey, index string) *OssDirectory {
	client, err := NewClient(endpoint, accessKeyID, secretAccessKey)
	if err != nil {
		return nil
	}
	return &OssDirectory{
		client: client,
		bucket: bucket,
		index:  index,
	}
}

func (d *OssDirectory) Setup(readOnly bool) error {
	defer utils.Timerf(
		"[directory] method:setup, type:oss, bucket:%s, index:%s, readOnly:%t",
		d.bucket,
		d.index,
		readOnly,
	)()
	exist, err := IsBucketExist(d.client, d.bucket)
	if err != nil {
		return err
	}
	if !exist {
		err := CreateBucket(d.client, d.bucket)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *OssDirectory) List(kind string) ([]uint64, error) {

	defer utils.Timerf(
		"[directory] method:list, type:oss, bucket:%s, index:%s, kind:%s",
		d.bucket,
		d.index,
		kind,
	)()

	d.lock.RLock()
	defer d.lock.RUnlock()

	dirEntries, err := ListObjects(d.client, d.bucket, ossPath(d.index))
	if err != nil {
		return nil, err
	}

	var rv uint64Slice
	for _, dirEntry := range dirEntries {
		if filepath.Ext(dirEntry.Key) != kind {
			continue
		}
		base := filepath.Base(dirEntry.Key)
		epoch, err := strconv.ParseUint(base[:len(base)-len(kind)], 16, 64)
		if err != nil {
			logger.Error(
				"oss list parse object fail",
				zap.String("index", d.index),
				zap.String("bucket", d.bucket),
				zap.String("key", dirEntry.Key),
				zap.Error(err),
			)
			return nil, err
		}
		rv = append(rv, epoch)
	}

	sort.Sort(sort.Reverse(rv))

	return rv, nil
}

func (d *OssDirectory) Persist(
	kind string,
	id uint64,
	w index.WriterTo,
	closeCh chan struct{},
) error {

	filename := d.fileName(kind, id)
	defer utils.Timerf(
		"[directory] method:persist, type:oss, bucket:%s, index:%s, filename:%s",
		d.bucket,
		d.index,
		filename,
	)()

	d.lock.Lock()
	defer d.lock.Unlock()

	var buf bytes.Buffer
	_, err := w.WriteTo(&buf, closeCh)
	if err != nil {
		logger.Error(
			"oss persist write buffer fail",
			zap.String("index", d.index),
			zap.String("bucket", d.bucket),
			zap.String("filename", filename),
			zap.Error(err),
		)
		return err
	}
	err = PutObject(d.client, d.bucket, ossKey(d.index, filename), &buf)
	if err != nil {
		return err
	}
	return nil
}

func (d *OssDirectory) Load(kind string, id uint64) (*segment.Data, io.Closer, error) {

	filename := d.fileName(kind, id)
	defer utils.Timerf(
		"[directory] method:load, type:oss, bucket:%s, index:%s, filename:%s",
		d.bucket,
		d.index,
		filename,
	)()

	d.lock.RLock()
	defer d.lock.RUnlock()

	key := ossKey(d.index, filename)
	object, err := GetObject(d.client, d.bucket, key)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		err := object.Close()
		if err != nil {
			logger.Error(
				"oss load close object fail",
				zap.String("index", d.index),
				zap.String("bucket", d.bucket),
				zap.String("key", key),
				zap.Error(err),
			)
		}
	}()
	objBytes, err := io.ReadAll(object)
	if err != nil {
		return nil, nil, err
	}
	return segment.NewDataBytes(objBytes), nil, nil
}

func (d *OssDirectory) Remove(kind string, id uint64) error {

	filename := d.fileName(kind, id)
	defer utils.Timerf(
		"[directory] method:remove, type:oss, bucket:%s, index:%s, filename:%s",
		d.bucket,
		d.index,
		filename,
	)()

	d.lock.Lock()
	defer d.lock.Unlock()

	err := DeleteObject(d.client, d.bucket, ossKey(d.index, filename))
	if err != nil {
		return err
	}
	return nil
}

// Lock ensures this process has exclusive access to write in this directory.
// We plan to restrict an OssDirectory to be accessed by at most one process at the same time
// through the first-level shard strategy (shard).
func (d *OssDirectory) Lock() error {
	return nil
}

// Unlock releases the lock held on this directory.
// We plan to restrict an OssDirectory to be accessed by at most one process at the same time
// through the first-level shard strategy (shard).
func (d *OssDirectory) Unlock() error {
	return nil
}

func (d *OssDirectory) Stats() (numFilesOnDisk, numBytesUsedDisk uint64) {

	defer utils.Timerf(
		"[directory] method:stats, type:oss, bucket:%s, index:%s",
		d.bucket,
		d.index,
	)()

	d.lock.RLock()
	defer d.lock.RUnlock()

	dirEntries, err := ListObjects(d.client, d.bucket, ossPath(d.index))
	if err != nil {
		return 0, 0
	}

	for _, obj := range dirEntries {
		numFilesOnDisk++
		numBytesUsedDisk += uint64(obj.Size)
	}

	return numFilesOnDisk, numBytesUsedDisk
}

func (d *OssDirectory) Sync() error {
	defer utils.Timerf(
		"[directory] method:sync, type:oss, bucket:%s, index:%s",
		d.bucket,
		d.index,
	)()
	return nil
}

func (d *OssDirectory) fileName(kind string, id uint64) string {
	return fmt.Sprintf("%012x", id) + kind
}

func ossPath(index string) string {
	return fmt.Sprintf("%s/", index)
}

func ossKey(index, filename string) string {
	return fmt.Sprintf("%s/%s", index, filename)
}

type uint64Slice []uint64

func (e uint64Slice) Len() int           { return len(e) }
func (e uint64Slice) Swap(i, j int)      { e[i], e[j] = e[j], e[i] }
func (e uint64Slice) Less(i, j int) bool { return e[i] < e[j] }
