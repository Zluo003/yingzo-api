//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const minioIntegrationImage = "minio/minio:RELEASE.2025-04-22T22-12-26Z"

func TestTemporaryAssetMinIOUploadRangeAndCleanup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const (
		accessKey = "yingzo-integration"
		secretKey = "yingzo-integration-secret"
		bucket    = "yingzo-agent-assets-test"
	)
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        minioIntegrationImage,
			ExposedPorts: []string{"9000/tcp"},
			Env: map[string]string{
				"MINIO_ROOT_USER":     accessKey,
				"MINIO_ROOT_PASSWORD": secretKey,
			},
			Cmd:        []string{"server", "/data", "--address", ":9000"},
			WaitingFor: wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "9000/tcp")
	require.NoError(t, err)
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())
	client := newMinIOIntegrationClient(t, ctx, endpoint, accessKey, secretKey)
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	store, err := repository.NewS3BackupStoreFactory()(ctx, &service.BackupS3Config{
		Endpoint:        endpoint,
		Region:          "us-east-1",
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		Bucket:          bucket,
		ForcePathStyle:  true,
	})
	require.NoError(t, err)
	require.NoError(t, store.HeadBucket(ctx))

	h, mock := newAgentHandlerMock(t)
	h.objectStore = store
	png := onePixelPNG(t)
	expectTemporaryAssetInsert(mock, "s3", int64(len(png)))
	uploadRequest, _ := multipartRequest(t, "pixel.png", "image/png", png)
	uploadRequest.Host = "api-key.cc"
	upload := httptest.NewRecorder()
	uploadRouter(h).ServeHTTP(upload, uploadRequest)
	require.Equal(t, http.StatusCreated, upload.Code, upload.Body.String())

	listed, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	require.Len(t, listed.Contents, 1)
	storageKey := aws.ToString(listed.Contents[0].Key)
	require.True(t, strings.HasPrefix(storageKey, "agent-assets/"))
	assetID, publicURL := temporaryAssetIdentityFromResponse(t, upload)
	require.Equal(t, "https://api-key.cc/media/"+assetID.String()+"/asset.png", publicURL)
	require.NotContains(t, publicURL, "?")

	mock.ExpectQuery("SELECT storage_backend,storage_key,original_filename,mime_type,size_bytes,").
		WithArgs(assetID).
		WillReturnRows(sqlmock.NewRows([]string{"storage_backend", "storage_key", "original_filename", "mime_type", "size_bytes", "expires_at"}).
			AddRow("s3", storageKey, "pixel.png", "image/png", len(png), time.Now().Add(time.Hour)))
	mock.ExpectExec("UPDATE temporary_assets SET last_accessed_at").
		WithArgs(assetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	router := gin.New()
	router.GET("/media/:id/:filename", h.ServeCleanTemporaryAsset)
	rangeResponse := httptest.NewRecorder()
	rangeRequest := httptest.NewRequest(http.MethodGet, "/media/"+assetID.String()+"/asset.png", nil)
	rangeRequest.Header.Set("Range", "bytes=4-7")
	router.ServeHTTP(rangeResponse, rangeRequest)
	require.Equal(t, http.StatusPartialContent, rangeResponse.Code)
	require.Equal(t, png[4:8], rangeResponse.Body.Bytes())

	cleanupAssetID := uuid.New()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,storage_backend,storage_key FROM temporary_assets").
		WillReturnRows(sqlmock.NewRows([]string{"id", "storage_backend", "storage_key"}).AddRow(cleanupAssetID, "s3", storageKey))
	mock.ExpectExec("UPDATE temporary_assets SET deleted_at").
		WithArgs(cleanupAssetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("DELETE FROM agent_generation_quotes").WillReturnResult(sqlmock.NewResult(0, 0))
	cleaned, err := h.CleanupExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), cleaned)

	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(storageKey)})
	require.Error(t, err)
	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Contains(t, []string{"NoSuchKey", "NotFound"}, apiErr.ErrorCode())
	require.NoError(t, mock.ExpectationsWereMet())
}

func newMinIOIntegrationClient(t *testing.T, ctx context.Context, endpoint, accessKey, secretKey string) *s3.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	require.NoError(t, err)
	return s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
}

func temporaryAssetIdentityFromResponse(t *testing.T, response *httptest.ResponseRecorder) (uuid.UUID, string) {
	t.Helper()
	var body struct {
		ID  uuid.UUID `json:"id"`
		URL string    `json:"url"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotEqual(t, uuid.Nil, body.ID)
	return body.ID, body.URL
}
