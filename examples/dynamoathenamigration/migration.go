// Copyright 2023 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dynamoathenamigration

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamoTypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"golang.org/x/sync/errgroup"

	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/events/athena"
	"github.com/gravitational/teleport/lib/utils/prompt"
)

type Config struct {
	// ExportTime is time in the past from which to export table data.
	ExportTime time.Time

	// ExportARN allows to use already finished export without triggering new.
	ExportARN string

	// DynamoTableARN that will be exported.
	DynamoTableARN string

	// ExportLocalDir specifies where export files will be downloaded (it must exists).
	// If empty os.TempDir() will be used.
	ExportLocalDir string

	// Bucket used to store export.
	Bucket string
	// Prefix is s3 prefix where to store export inside bucket.
	Prefix string

	// DryRun allows to generate export and convert it to AuditEvents.
	// Nothing is published to athena publisher.
	// Can be used to test if export is valid.
	DryRun bool

	// NoOfEmitWorkers defines how many workers are used to emit audit events.
	NoOfEmitWorkers int
	bufferSize      int

	// CheckpointPath is full path of file where checkpoint data should be stored.
	// Defaults to file in current directory (athenadynamomigration.json)
	// Checkpoint allow to resume export which failed during emitting.
	CheckpointPath string

	// TopicARN is topic of athena logger.
	TopicARN string
	// LargePayloadBucket is s3 bucket configured for large payloads in athena logger.
	LargePayloadBucket string
	// LargePayloadPrefix is s3 prefix configured for large payloads in athena logger.
	LargePayloadPrefix string

	Logger log.FieldLogger
}

const defaultCheckpointPath = "athenadynamomigration.json"

func (cfg *Config) CheckAndSetDefaults() error {
	if cfg.ExportTime.IsZero() {
		cfg.ExportTime = time.Now()
	}
	if cfg.DynamoTableARN == "" && cfg.ExportARN == "" {
		return trace.BadParameter("either DynamoTableARN or ExportARN is required")
	}
	if cfg.Bucket == "" {
		return trace.BadParameter("missing export bucket")
	}
	if cfg.NoOfEmitWorkers == 0 {
		cfg.NoOfEmitWorkers = 3
	}
	if cfg.bufferSize == 0 {
		cfg.bufferSize = 10 * cfg.NoOfEmitWorkers
	}
	if !cfg.DryRun {
		if cfg.TopicARN == "" {
			return trace.BadParameter("missing Athena logger SNS Topic ARN")
		}

		if cfg.LargePayloadBucket == "" {
			return trace.BadParameter("missing Athena logger large payload bucket")
		}
	}
	if cfg.CheckpointPath == "" {
		cfg.CheckpointPath = defaultCheckpointPath
	}

	if cfg.Logger == nil {
		cfg.Logger = log.New()
	}
	return nil
}

type task struct {
	Config
	dynamoClient  *dynamodb.Client
	s3Downloader  s3downloader
	eventsEmitter eventsEmitter
}

type s3downloader interface {
	Download(ctx context.Context, w io.WriterAt, input *s3.GetObjectInput, options ...func(*manager.Downloader)) (n int64, err error)
}

type eventsEmitter interface {
	EmitAuditEvent(ctx context.Context, in apievents.AuditEvent) error
}

func newMigrateTask(ctx context.Context, cfg Config) (*task, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	s3Client := s3.NewFromConfig(awsCfg)
	return &task{
		Config:       cfg,
		dynamoClient: dynamodb.NewFromConfig(awsCfg),
		s3Downloader: manager.NewDownloader(s3Client),
		eventsEmitter: athena.NewPublisher(athena.PublisherConfig{
			TopicARN: cfg.TopicARN,
			SNSPublisher: sns.NewFromConfig(awsCfg, func(o *sns.Options) {
				o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
					so.MaxAttempts = 30
					so.MaxBackoff = 1 * time.Minute
					// Use bigger rate limit to handle default sdk throttling: https://github.com/aws/aws-sdk-go-v2/issues/1665
					so.RateLimiter = ratelimit.NewTokenRateLimit(1000000)
				})
			}),
			Uploader:      manager.NewUploader(s3Client),
			PayloadBucket: cfg.LargePayloadBucket,
			PayloadPrefix: cfg.LargePayloadPrefix,
		}),
	}, nil
}

// Migrate executed dynamodb -> athena migration.
func Migrate(ctx context.Context, cfg Config) error {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return trace.Wrap(err)
	}

	t, err := newMigrateTask(ctx, cfg)
	if err != nil {
		return trace.Wrap(err)
	}

	exportInfo, err := t.GetOrStartExportAndWaitForResults(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	if err := t.ProcessDataObjects(ctx, exportInfo); err != nil {
		return trace.Wrap(err)
	}

	t.Logger.Info("Migration finished")
	return nil
}

// GetOrStartExportAndWaitForResults return export results.
// It can either reused existing export or start new one, depending on FreshnessWindow.
func (t *task) GetOrStartExportAndWaitForResults(ctx context.Context) (*exportInfo, error) {
	exportARN := t.Config.ExportARN
	if exportARN == "" {
		var err error
		exportARN, err = t.startExportJob(ctx)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}

	manifest, err := t.waitForCompletedExport(ctx, exportARN)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	t.Logger.Debugf("Using export manifest %s", manifest)
	dataObjectsInfo, err := t.getDataObjectsInfo(ctx, manifest)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &exportInfo{
		ExportARN:       exportARN,
		DataObjectsInfo: dataObjectsInfo,
	}, nil
}

// ProcessDataObjects takes dataObjectInfo from export summary, downloads data files
// from s3, ungzip them and emitt them on SNS using athena publisher.
func (t *task) ProcessDataObjects(ctx context.Context, exportInfo *exportInfo) error {
	eventsC := make(chan apievents.AuditEvent, t.bufferSize)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		err := t.getEventsFromDataFiles(egCtx, exportInfo, eventsC)
		close(eventsC)
		return trace.Wrap(err)
	})

	eg.Go(func() error {
		err := t.emitEvents(egCtx, eventsC, exportInfo.ExportARN)
		return trace.Wrap(err)
	})

	return trace.Wrap(eg.Wait())
}

func (t *task) waitForCompletedExport(ctx context.Context, exportARN string) (exportManifest string, err error) {
	req := &dynamodb.DescribeExportInput{
		ExportArn: aws.String(exportARN),
	}
	for {
		exportStatusOutput, err := t.dynamoClient.DescribeExport(ctx, req)
		if err != nil {
			return "", trace.Wrap(err)
		}

		if exportStatusOutput == nil || exportStatusOutput.ExportDescription == nil {
			return "", errors.New("dynamo DescribeExport returned unexpected nil on response")
		}

		exportStatus := exportStatusOutput.ExportDescription.ExportStatus
		switch exportStatus {
		case dynamoTypes.ExportStatusCompleted:
			return aws.ToString(exportStatusOutput.ExportDescription.ExportManifest), nil
		case dynamoTypes.ExportStatusFailed:
			return "", trace.Errorf("export %s returned failed status", exportARN)
		case dynamoTypes.ExportStatusInProgress:
			select {
			case <-ctx.Done():
				return "", trace.Wrap(ctx.Err())
			case <-time.After(10 * time.Second):
				t.Logger.Debug("Export job still in progress...")
			}
		}

	}
}

func (t *task) startExportJob(ctx context.Context) (arn string, err error) {
	exportOutput, err := t.dynamoClient.ExportTableToPointInTime(ctx, &dynamodb.ExportTableToPointInTimeInput{
		S3Bucket:     aws.String(t.Bucket),
		TableArn:     aws.String(t.DynamoTableARN),
		ExportFormat: dynamoTypes.ExportFormatDynamodbJson,
		ExportTime:   aws.Time(t.ExportTime),
		S3Prefix:     aws.String(t.Prefix),
	})
	if err != nil {
		return "", trace.Wrap(err)
	}
	if exportOutput == nil || exportOutput.ExportDescription == nil {
		return "", errors.New("dynamo ExportTableToPointInTime returned unexpected nil on response")
	}

	exportArn := aws.ToString(exportOutput.ExportDescription.ExportArn)
	t.Logger.Infof("Started export %s", exportArn)
	return exportArn, nil
}

type exportInfo struct {
	ExportARN       string
	DataObjectsInfo []dataObjectInfo
}

type dataObjectInfo struct {
	DataFileS3Key string `json:"dataFileS3Key"`
	ItemCount     int    `json:"itemCount"`
}

// getDataObjectsInfo downloads manifest-files.json and get data object info from it.
func (t *task) getDataObjectsInfo(ctx context.Context, manifestPath string) ([]dataObjectInfo, error) {
	// summary file is small, we can use in-memory buffer.
	writeAtBuf := manager.NewWriteAtBuffer([]byte{})
	if _, err := t.s3Downloader.Download(ctx, writeAtBuf, &s3.GetObjectInput{
		Bucket: aws.String(t.Bucket),
		// AWS SDK returns manifest-summary.json path. We are interested in
		// manifest-files.json because it's contains references about data export files.
		Key: aws.String(path.Dir(manifestPath) + "/manifest-files.json"),
	}); err != nil {
		return nil, trace.Wrap(err)
	}

	var out []dataObjectInfo
	scanner := bufio.NewScanner(bytes.NewBuffer(writeAtBuf.Bytes()))
	// manifest-files are JSON lines files, that why it's scanned line by line.
	for scanner.Scan() {
		var obj dataObjectInfo
		err := json.Unmarshal(scanner.Bytes(), &obj)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		out = append(out, obj)
	}
	if err := scanner.Err(); err != nil {
		return nil, trace.Wrap(err)
	}
	return out, nil
}

func (t *task) getEventsFromDataFiles(ctx context.Context, exportInfo *exportInfo, eventsC chan<- apievents.AuditEvent) error {
	checkpoint, err := t.loadEmitterCheckpoint(ctx, exportInfo.ExportARN)
	if err != nil {
		return trace.Wrap(err)
	}

	if checkpoint != nil {
		if checkpoint.FinishedWithError {
			reuse, err := prompt.Confirmation(ctx, os.Stdout, prompt.Stdin(), fmt.Sprintf("It seems that previous migration %s stopped with error, do you want to resume it?", exportInfo.ExportARN))
			if err != nil {
				return trace.Wrap(err)
			}
			if reuse {
				t.Logger.Info("Resuming emitting from checkpoint")
			} else {
				// selected not reuse
				checkpoint = nil
			}
		} else {
			// migration completed without any error, no sense of reusing checkpoint.
			t.Logger.Info("Skipping checkpoint because previous migration finished without error")
			checkpoint = nil
		}
	}

	// afterCheckpoint is used to pass information between fromS3ToChan calls
	// if checkpoint was reached.
	var afterCheckpoint bool
	for _, dataObj := range exportInfo.DataObjectsInfo {
		t.Logger.Debugf("Downloading %s", dataObj.DataFileS3Key)
		afterCheckpoint, err = t.fromS3ToChan(ctx, dataObj, eventsC, checkpoint, afterCheckpoint)
		if err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

func (t *task) fromS3ToChan(ctx context.Context, dataObj dataObjectInfo, eventsC chan<- apievents.AuditEvent, checkpoint *checkpointData, afterCheckpointIn bool) (afterCheckpointOut bool, err error) {
	f, err := t.downloadFromS3(ctx, dataObj.DataFileS3Key)
	if err != nil {
		return false, trace.Wrap(err)
	}
	defer f.Close()

	gzipReader, err := gzip.NewReader(f)
	if err != nil {
		return false, trace.Wrap(err)
	}
	defer gzipReader.Close()

	checkpointValues := checkpoint.checkpointValues()
	afterCheckpoint := afterCheckpointIn

	scanner := bufio.NewScanner(gzipReader)
	t.Logger.Debugf("Scanning %d events", dataObj.ItemCount)
	count := 0
	for scanner.Scan() {
		count++
		ev, err := exportedDynamoItemToAuditEvent(ctx, scanner.Bytes())
		if err != nil {
			return false, trace.Wrap(err)
		}

		// if checkpoint is present, it means that previous run ended with error
		// and we want to continue from last valid checkpoint.
		// We have list of checkpoints because processing is done in async way with
		// multiple workers. We are looking for first id among checkpoints.
		if checkpoint != nil && !afterCheckpoint {
			if !slices.Contains(checkpointValues, ev.GetID()) {
				// skipping because was processed in previous run.
				continue
			} else {
				t.Logger.Debugf("Event %s is last checkpoint, will start emitting from next event on the list", ev.GetID())
				// id is on list of valid checkpoints
				afterCheckpoint = true
				// This was last completed, skip it and from next iteration emit everything.
				continue
			}
		}

		select {
		case eventsC <- ev:
		case <-ctx.Done():
			return false, ctx.Err()
		}

		if count%100 == 0 {
			t.Logger.Debugf("Sent on buffer %d/%d events from %s", count, dataObj.ItemCount, dataObj.DataFileS3Key)
		}
	}

	if err := scanner.Err(); err != nil {
		return false, trace.Wrap(err)
	}
	return afterCheckpoint, nil
}

// exportedDynamoItemToAuditEvent converts single line of dynamo export into AuditEvent.
func exportedDynamoItemToAuditEvent(ctx context.Context, in []byte) (apievents.AuditEvent, error) {
	var itemMap map[string]map[string]any
	if err := json.Unmarshal(in, &itemMap); err != nil {
		return nil, trace.Wrap(err)
	}

	var attributeMap map[string]dynamoTypes.AttributeValue
	if err := awsAwsjson10_deserializeDocumentAttributeMap(&attributeMap, itemMap["Item"]); err != nil {
		return nil, trace.Wrap(err)
	}

	var eventFields events.EventFields
	if err := attributevalue.Unmarshal(attributeMap["FieldsMap"], &eventFields); err != nil {
		return nil, trace.Wrap(err)
	}

	event, err := events.FromEventFields(eventFields)
	return event, trace.Wrap(err)
}

func (t *task) downloadFromS3(ctx context.Context, key string) (*os.File, error) {
	originalName := path.Base(key)

	var dir string
	if t.Config.ExportLocalDir != "" {
		dir = t.Config.ExportLocalDir
	} else {
		dir = os.TempDir()
	}
	path := path.Join(dir, originalName)

	f, err := os.Create(path)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if _, err := t.s3Downloader.Download(ctx, f, &s3.GetObjectInput{
		Bucket: aws.String(t.Bucket),
		Key:    aws.String(key),
	}); err != nil {
		f.Close()
		return nil, trace.Wrap(err)
	}
	return f, nil
}

type checkpointData struct {
	ExportARN         string `json:"export_arn"`
	FinishedWithError bool   `json:"finished_with_error"`
	// Checkpoints key represents worker index.
	// Checkpoints value represents last valid event id.
	Checkpoints map[int]string `json:"checkpoints"`
}

func (c *checkpointData) checkpointValues() []string {
	if c == nil {
		return nil
	}
	return maps.Values(c.Checkpoints)
}

func (t *task) storeEmitterCheckpoint(in checkpointData) error {
	bb, err := json.Marshal(in)
	if err != nil {
		return trace.Wrap(err)
	}
	return trace.Wrap(os.WriteFile(t.CheckpointPath, bb, 0o755))
}

func (t *task) loadEmitterCheckpoint(ctx context.Context, exportARN string) (*checkpointData, error) {
	bb, err := os.ReadFile(t.CheckpointPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, trace.Wrap(err)
	}
	var out checkpointData
	if err := json.Unmarshal(bb, &out); err != nil {
		return nil, trace.Wrap(err)
	}

	// There are checkpoints for different export, assume there is no checkpoint saved.
	if exportARN != out.ExportARN {
		return nil, nil
	}

	return &out, nil
}

func (t *task) emitEvents(ctx context.Context, eventsC <-chan apievents.AuditEvent, exportARN string) error {
	if t.DryRun {
		// in dryRun we just want to count events, validation is done when reading from file.
		var count int
		var oldest, newest apievents.AuditEvent
		for event := range eventsC {
			count++
			if oldest == nil && newest == nil {
				// first iteration, initialize values with first event.
				oldest = event
				newest = event
			}
			if oldest.GetTime().After(event.GetTime()) {
				oldest = event
			}
			if newest.GetTime().Before(event.GetTime()) {
				newest = event
			}
		}
		if count == 0 {
			return errors.New("there were not events from export")
		}
		t.Logger.Infof("Dry run: there are %d events from %v to %v", count, oldest.GetTime(), newest.GetTime())
		return nil
	}
	// mu protects checkpointsPerWorker.
	var mu sync.Mutex
	checkpointsPerWorker := map[int]string{}

	errG, workerCtx := errgroup.WithContext(ctx)

	for i := 0; i < t.NoOfEmitWorkers; i++ {
		i := i
		errG.Go(func() error {
			for {
				select {
				case <-workerCtx.Done():
					return trace.Wrap(ctx.Err())
				case e, ok := <-eventsC:
					if !ok {
						return nil
					}
					if err := t.eventsEmitter.EmitAuditEvent(workerCtx, e); err != nil {
						return trace.Wrap(err)
					} else {
						mu.Lock()
						checkpointsPerWorker[i] = e.GetID()
						mu.Unlock()
					}
				}
			}
		})
	}

	workersErr := errG.Wait()
	// workersErr is handled below because we want to store checkpoint on error.

	// If there is missing data from at least one worker, it means that worker
	// does not have any valid checkpoint to store. Without any valid checkpoint
	// we won't be able to calculate min checkpoint, so does not store checkpoint at all.
	if len(checkpointsPerWorker) < t.NoOfEmitWorkers {
		t.Logger.Warnf("Not enough checkpoints from workers, got %d, expected %d", len(checkpointsPerWorker), t.NoOfEmitWorkers)
		return trace.Wrap(workersErr)
	}

	checkpoint := checkpointData{
		FinishedWithError: workersErr != nil || ctx.Err() != nil,
		ExportARN:         exportARN,
		Checkpoints:       checkpointsPerWorker,
	}
	if err := t.storeEmitterCheckpoint(checkpoint); err != nil {
		t.Logger.Errorf("Failed to store checkpoint: %v", err)
	}
	return trace.Wrap(workersErr)
}
