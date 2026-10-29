package domain

type IdempotencyRecord struct {
	Key         string `json:"key"`
	RequestHash string `json:"requestHash"`
	TransferID  string `json:"transferId"`
}
