package store

import "strconv"

// ConnectionAAD binds the sealed GitHub token to its row, so a ciphertext cannot be moved to another
// one. Tokens that are already sealed in a database depend on this exact value: never change it.
func ConnectionAAD() string { return "github_connection:" + strconv.FormatInt(ConnectionID, 10) }

// UndecryptableDetail is what the UI shows when the master key does not open the stored token.
const UndecryptableDetail = "The stored token cannot be decrypted. Check REMEDY_MASTER_KEY or enter the token again."
