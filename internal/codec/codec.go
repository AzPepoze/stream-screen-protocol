package codec

// Encoder compresses raw frame data
type Encoder interface {
	// Encode encodes raw RGBA frame to codec-specific format
	// Returns: compressed data, error
	Encode(rgbaData []byte, width, height int) ([]byte, error)

	// CodecName returns the codec identifier
	CodecName() string

	// Close cleans up encoder resources
	Close() error
}

// Decoder decompresses codec-specific data
type Decoder interface {
	// Decode decodes codec-specific data to raw RGBA
	// Returns: RGBA frame data, error
	Decode(encodedData []byte, width, height int) ([]byte, error)

	// CodecName returns the codec identifier
	CodecName() string

	// Close cleans up decoder resources
	Close() error
}

// Config holds codec-specific configuration
type Config map[string]interface{}

// Get retrieves a config value by key with a default
func (c Config) Get(key string, defaultValue interface{}) interface{} {
	if val, ok := c[key]; ok {
		return val
	}
	return defaultValue
}

// GetInt retrieves an integer config value
func (c Config) GetInt(key string, defaultValue int) int {
	if val, ok := c[key]; ok {
		if intVal, ok := val.(int); ok {
			return intVal
		}
		if floatVal, ok := val.(float64); ok {
			return int(floatVal)
		}
	}
	return defaultValue
}

// GetString retrieves a string config value
func (c Config) GetString(key string, defaultValue string) string {
	if val, ok := c[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return defaultValue
}
