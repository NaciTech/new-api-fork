package relay

import (
	"bytes"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func relayPassThroughBodyWithChannelAffinity(c *gin.Context, storage common.BodyStorage) (io.Reader, int64, *types.NewAPIError) {
	if !service.HasChannelAffinityJSONMapping(c) {
		return common.ReaderOnly(storage), storage.Size(), nil
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, 0, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	out, err := service.ApplyChannelAffinityJSONMapping(c, body)
	if err != nil {
		return nil, 0, types.NewErrorWithStatusCode(err, types.ErrorCodeConvertRequestFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return bytes.NewReader(out), int64(len(out)), nil
}
