# Overall device state, published periodically.

uint8 MODE_IDLE=0
uint8 MODE_ACTIVE=1
uint8 MODE_FAULT=2

# Currently active mode, one of the MODE_* constants.
uint8 mode
# Human-readable device label.
string<=32 label
bool tracking false
# Publish rate the device is configured for.
float64 rate_hz 10.0
# Most recent per-channel readings.
Reading[<=8] recent
float32[3] accel
uint32[] event_ids
