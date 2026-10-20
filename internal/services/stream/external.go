// SPDX-License-Identifier: GPL-3.0-or-later

package stream

import (
	"context"
	"io"

	"github.com/bilirec/bilirec/pkg/pool"
)

// ReadExternalStream pumps the FLV output of an externally spawned downloader
// (ffmpeg / streamlink hybrid) into a chunk channel.
//
// It has the same idle-timeout and channel semantics as ReadFlvStream: buffers
// come from chunkPool, the returned channel is closed when the pump exits, and
// both rc and the pool lease are released on exit. Closing rc (for example
// from the idle-timeout wrapper) must kill the downloader process tree.
func (r *Service) ReadExternalStream(
	rc io.ReadCloser,
	ctx context.Context,
	qn int,
	chunkPool *pool.BucketedBytesPool,
	releasePool func(),
) <-chan []byte {
	readSize := r.readBufSizeForQn(qn)
	ch := make(chan []byte, r.chanBufferSizeForQn(qn))
	go r.readFlv(ch, rc, ctx, chunkPool, readSize, releasePool)
	return ch
}
