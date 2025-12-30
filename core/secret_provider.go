package core

// NoOpSecretProvider 默认的明文透传 SecretProvider (Task 4 Placeholder)
type NoOpSecretProvider struct{}

func NewNoOpSecretProvider() *NoOpSecretProvider {
	return &NoOpSecretProvider{}
}

func (s *NoOpSecretProvider) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

func (s *NoOpSecretProvider) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}
