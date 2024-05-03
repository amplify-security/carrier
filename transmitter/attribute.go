package transmitter

type (
	// TransmitAttributes is a map of key-value pairs that may be sent with message transmission.
	// These attributes may be sent or ignored depending on the Transmitter implementation.
	TransmitAttributes map[string]string
)
