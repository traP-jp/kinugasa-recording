# RIST receive buffers

The gateway uses a 5,000 ms recovery window (both minimum and maximum), and
waits at least 200 ms for reordered packets before requesting retransmission
(librist also considers half the measured, bounded RTT). Override
these with `--recovery-buffer-ms` and `--reorder-buffer-ms`; the reorder window
must be smaller than the recovery window. The CameraConnection operator passes
these values explicitly to camera Pods. The gateway logs both values at startup.

These settings favor recovery over latency: compared with the previous 1,000 ms
recovery window and librist's 15 ms reorder default, live preview is delayed by
approximately four additional seconds. Sender-side retention must also be long
enough to service retransmission requests; receiver settings cannot configure a
remote camera's sender buffer.

`ReceiverConfig.max_jitter` is left at the librist default of 5 ms. It controls processing
timing, not the amount of network jitter the recovery window can absorb.
Increasing it is not a substitute for increasing the recovery buffer.

The pinned rist-rs receiver uses data callbacks and explicitly disables librist's
polling output FIFO with `rist_receiver_set_output_fifo_size(ctx, 0)`. There is
no active output FIFO to enlarge in this receive path.

Changing the operator's Pod arguments requires updated console-server and
video-gateway images. Existing camera Pods must be recreated to pick up new
arguments; coordinate this with recording and pending-upload handling before
rollout. Increasing buffers does not fix recording finalization or upload-state
handling when MediaMTX restarts a recording segment.
