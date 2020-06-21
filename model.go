package tinkoff

import sdk "github.com/TinkoffCreditSystems/invest-openapi-go-sdk"

type TcfBalanceItem struct {
	FIGI                    string
	Name                    string
	Ticker                  string
	Currency                string
	OperationAmount         float64
	BrokerCommissionAmount  float64
	CurrentPrice            float64
	PortfolioAmount         float64
	PortfolioQuantity       int
	DividendAmount          float64
	DividendTaxAmount       float64
	ServiceCommissionAmount float64
	BalanceAmount           float64
}

type TcfTotal struct {
	BalanceAmount           float64
	ServiceCommissionAmount float64
	TaxBack                 float64
	PortfolioAmount         float64
}

type TcfBalanceTotal struct {
	Currencies map[string]*TcfTotal
}

type TcfPortfolioBalance struct {
	Items []*TcfBalanceItem
	Total *TcfBalanceTotal
}

func createEmptyBalance() *TcfPortfolioBalance {

	total := &TcfBalanceTotal{
		Currencies: make(map[string]*TcfTotal),
	}

	total.Currencies["RUB"] = &TcfTotal{BalanceAmount: 0.0, ServiceCommissionAmount: 0.0, TaxBack: 0.0}
	total.Currencies["USD"] = &TcfTotal{BalanceAmount: 0.0, ServiceCommissionAmount: 0.0, TaxBack: 0.0}
	total.Currencies["EUR"] = &TcfTotal{BalanceAmount: 0.0, ServiceCommissionAmount: 0.0, TaxBack: 0.0}

	balance := &TcfPortfolioBalance{Items: []*TcfBalanceItem{}, Total: total}

	return balance
}

func createBalanceItem(instrument *sdk.SearchInstrument) *TcfBalanceItem {
	balanceItem := &TcfBalanceItem{
		FIGI:                    instrument.FIGI,
		Ticker:                  instrument.Ticker,
		Name:                    instrument.Name,
		Currency:                string(instrument.Currency),
		BalanceAmount:           0.0,
		OperationAmount:         0.0,
		BrokerCommissionAmount:  0.0,
		PortfolioQuantity:       0,
		PortfolioAmount:         0.0,
		DividendAmount:          0.0,
		DividendTaxAmount:       0.0,
		ServiceCommissionAmount: 0.0,
	}
	return balanceItem
}
