package fnvhash

const Offset64 = uint64(1469598103934665603)

func MixString(hash uint64, value string) uint64 {
	hash = MixUint64(hash, uint64(len(value)))
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= 1099511628211
	}
	return hash
}

func MixUint64(hash uint64, value uint64) uint64 {
	return Avalanche(hash ^ Avalanche(value+0x9e3779b97f4a7c15))
}

func Avalanche(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}
