package api

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	sdk "github.com/TinkoffCreditSystems/invest-openapi-go-sdk"
)

type TcfAccount struct {
	Client *sdk.RestClient
	Token  string
}

type TcfProfitRequest struct {
	PeriodFrom        time.Time
	PeriodTo          time.Time
	Figi              string
	ForWholePortfolio bool
}

type TcfProfitResponse struct {
	Figi        string
	ProfitTotal float32
	PeriodFrom  time.Time
	PeriodTo    time.Time
}

func InitAccount(token string) *TcfAccount {
	a := &TcfAccount{
		Token:  token,
		Client: sdk.NewRestClient(token),
	}
	return a
}

func (acc *TcfAccount) GetProfit(request *TcfProfitRequest) ([]TcfProfitResponse, error) {

	// get account's positions
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	positions, err := acc.Client.PositionsPortfolio(ctx, "")
	if err != nil {
		return nil, err
	}

	fmt.Println(positions)

	return nil, nil
}

var letterRunes = []rune("abcdefghzABCDEFOPQRSTUVWXYZ")

// Генерируем уникальный ID для запроса
func requestID() string {
	b := make([]rune, 12)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}

	return string(b)
}
