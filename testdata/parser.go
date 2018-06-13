package testdata

type (
	//zenrpc
	ParserService interface {
		Validate(message []byte, encoder int) (bool, error)
	}
)
