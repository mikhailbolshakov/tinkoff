package tinkoff

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	sdk "github.com/TinkoffCreditSystems/invest-openapi-go-sdk"
)

type TcfAccount struct {
	Client *sdk.RestClient
	Token  string
}

type TcfPortfolioBalanceRequest struct {
	PeriodFrom   time.Time
	PeriodTo     time.Time
	Figi         string
	ForPortfolio bool
	ExcludeFIGIs []string
}

type TcfGetOperationsRequest struct {
	PeriodFrom   time.Time
	PeriodTo     time.Time
	Figi         string
	ForPortfolio bool
	ExcludeFIGIs []string
}

func InitAccount(token string) *TcfAccount {
	a := &TcfAccount{
		Token:  token,
		Client: sdk.NewRestClient(token),
	}
	return a
}

func contains(slice []string, item string) bool {
	for _, elem := range slice {
		if elem == item {
			return true
		}
	}
	return false
}

type filterOperationsCriteria struct {
	FIGIs          []string
	Status         string
	OperationTypes []string
	ExcludeFIGIs   []string
}

func filterOperations(operations []sdk.Operation, criteria *filterOperationsCriteria) []sdk.Operation {

	res := []sdk.Operation{}

	if criteria == nil {
		return res
	}

	for _, oper := range operations {

		if (len(criteria.FIGIs) == 0 || contains(criteria.FIGIs, oper.FIGI)) &&
			(criteria.Status == "" || string(oper.Status) == criteria.Status) &&
			(len(criteria.OperationTypes) == 0 || contains(criteria.OperationTypes, string(oper.OperationType))) &&
			(len(criteria.ExcludeFIGIs) == 0 || !contains(criteria.ExcludeFIGIs, oper.FIGI)) {
			res = append(res, oper)
		}

	}

	return res

}

func aggOperationsByFigi(operations []sdk.Operation) map[string][]sdk.Operation {

	agg := make(map[string][]sdk.Operation)

	for _, oper := range operations {
		if oper.FIGI != "" {
			agg[oper.FIGI] = append(agg[oper.FIGI], oper)
		}
	}

	return agg
}

func candleLatest(candles []sdk.Candle) *sdk.Candle {

	sort.SliceStable(candles, func(i, j int) bool {
		return time.Time.After(candles[i].TS, candles[j].TS)
	})

	for _, candle := range candles {
		return &candle
	}

	return nil

}

func (acc *TcfAccount) GetCurrentPrice(figi string) (float64, error) {

	type candleRq struct {
		Interval   sdk.CandleInterval
		DurationFn func() time.Duration
		TruncateFn func() time.Duration
	}

	requests := []candleRq{
		{Interval: sdk.CandleInterval1Min, DurationFn: func() time.Duration { return -60 * time.Minute }, TruncateFn: func() time.Duration { return time.Minute }},
		{Interval: sdk.CandleInterval1Hour, DurationFn: func() time.Duration { return -24 * time.Hour }, TruncateFn: func() time.Duration { return time.Hour }},
		{Interval: sdk.CandleInterval1Day, DurationFn: func() time.Duration { return -24 * 7 * time.Hour }, TruncateFn: func() time.Duration { return time.Hour }},
	}

	var from time.Time
	var to time.Time
	var now time.Time
	var interval sdk.CandleInterval

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, rq := range requests {

		now = time.Now().Truncate(rq.TruncateFn())
		from = now.Add(rq.DurationFn())
		to = now
		interval = rq.Interval

		candles, err := acc.Client.Candles(ctx, from, to, interval, figi)
		if err != nil {
			return 0.0, err
		}

		candle := candleLatest(candles)

		if candle != nil && candle.ClosePrice != 0.0 {
			return candle.ClosePrice, nil
		}

	}

	return 0.0, errors.New(fmt.Sprintf("Current price cannot be determined for FIGI %s and period (%v %v %v). Candles aren't available", figi, from, to, interval))

}

func (acc *TcfAccount) GetByFigi(figi string) (*sdk.SearchInstrument, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	instrument, err := acc.Client.SearchInstrumentByFIGI(ctx, figi)
	if err != nil {
		return nil, err
	}

	return &instrument, nil

}

func (acc *TcfAccount) balanceItemToCh(
	figi string,
	operations []sdk.Operation,
	balanceItemCh chan<- *TcfBalanceItem,
	errorCh chan<- error) {

	go func() {

		figiOperations := filterOperations(operations, &filterOperationsCriteria{FIGIs: []string{figi}})

		currentPrice, err := acc.GetCurrentPrice(figi)
		if err != nil {
			errorCh <- err
			return
		}

		instrument, err := acc.GetByFigi(figi)
		if err != nil {
			errorCh <- err
			return
		}

		balanceItem := createBalanceItem(instrument)
		balanceItem.CurrentPrice = currentPrice

		for _, operation := range filterOperations(figiOperations, &filterOperationsCriteria{OperationTypes: []string{"Buy", "BuyCard", "Sell"}}) {

			sign := 1.0
			if operation.OperationType == "Sell" {
				sign = -1.0
			}

			balanceItem.BrokerCommissionAmount += math.Abs(operation.Commission.Value)
			balanceItem.OperationAmount += sign * math.Abs(operation.Payment)
			balanceItem.PortfolioQuantity += int(sign) * operation.Quantity
		}

		if balanceItem.PortfolioQuantity < 0 {
			balanceItem.PortfolioQuantity = 0
		}

		balanceItem.PortfolioAmount = float64(balanceItem.PortfolioQuantity) * balanceItem.CurrentPrice

		// dividend
		for _, operation := range filterOperations(figiOperations, &filterOperationsCriteria{OperationTypes: []string{"Dividend"}}) {

			balanceItem.DividendAmount += math.Abs(operation.Payment)
		}

		// dividend tax
		for _, operation := range filterOperations(figiOperations, &filterOperationsCriteria{OperationTypes: []string{"TaxDividend"}}) {

			balanceItem.DividendTaxAmount += math.Abs(operation.Payment)
		}

		balanceItem.BrokerCommissionAmount = math.Round(100*balanceItem.BrokerCommissionAmount) / 100
		balanceItem.OperationAmount = math.Round(100*balanceItem.OperationAmount) / 100
		balanceItem.PortfolioAmount = math.Round(100*balanceItem.PortfolioAmount) / 100
		balanceItem.DividendAmount = math.Round(100*balanceItem.DividendAmount) / 100
		balanceItem.DividendTaxAmount = math.Round(100*balanceItem.DividendTaxAmount) / 100

		balanceItem.BalanceAmount = math.Round(100*(balanceItem.PortfolioAmount+balanceItem.DividendAmount-balanceItem.DividendTaxAmount-balanceItem.OperationAmount-balanceItem.BrokerCommissionAmount)) / 100

		balanceItemCh <- balanceItem

	}()

}

func (acc *TcfAccount) GetOperations(request *TcfGetOperationsRequest) ([]sdk.Operation, error) {

	// get operations for the given period
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	operations, err := acc.Client.Operations(ctx, sdk.DefaultAccount, request.PeriodFrom, request.PeriodTo, request.Figi)
	if err != nil {
		return nil, err
	}

	criteria := &filterOperationsCriteria{ExcludeFIGIs: request.ExcludeFIGIs, Status: "Done"}

	if request.ForPortfolio {
		ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		portfolio, err := acc.Client.Portfolio(ctx, sdk.DefaultAccount)
		if err != nil {
			return nil, err
		}

		criteria.FIGIs = append(criteria.FIGIs, "")
		for _, p := range portfolio.Positions {
			criteria.FIGIs = append(criteria.FIGIs, p.FIGI)
		}

	}

	operations = filterOperations(operations, criteria)

	return operations, nil

}

func (acc *TcfAccount) GetPortfolioBalance(request *TcfPortfolioBalanceRequest) (*TcfPortfolioBalance, error) {

	operations, err := acc.GetOperations(&TcfGetOperationsRequest{
		PeriodFrom:   request.PeriodFrom,
		PeriodTo:     request.PeriodTo,
		Figi:         request.Figi,
		ForPortfolio: request.ForPortfolio,
		ExcludeFIGIs: request.ExcludeFIGIs,
	})
	if err != nil {
		return nil, err
	}

	// aggregate all operations by FIGI
	aggOperations := aggOperationsByFigi(operations)

	// create balance object
	balance := createEmptyBalance()

	// create channels
	balanceItemsCh := make(chan *TcfBalanceItem)
	defer close(balanceItemsCh)
	errorCh := make(chan error)
	defer close(errorCh)

	// populate balance items channel
	for figi, operations := range aggOperations {
		acc.balanceItemToCh(figi, operations, balanceItemsCh, errorCh)
	}

	// handle balance items
	for i := 0; i < len(aggOperations); i++ {
		select {
		case balanceItem := <-balanceItemsCh:
			balance.Items = append(balance.Items, balanceItem)
			balance.Total.Currencies[balanceItem.Currency].BalanceAmount += balanceItem.BalanceAmount
			balance.Total.Currencies[balanceItem.Currency].PortfolioAmount += balanceItem.PortfolioAmount
		case err = <-errorCh:
			return nil, err
		case <-time.After(20 * time.Second):
			return nil, fmt.Errorf("Timeout error")
		}
	}

	// service commission
	for _, operation := range filterOperations(operations, &filterOperationsCriteria{OperationTypes: []string{"ServiceCommission"}}) {
		balance.Total.Currencies[string(operation.Currency)].ServiceCommissionAmount += math.Abs(operation.Payment)
		balance.Total.Currencies[string(operation.Currency)].BalanceAmount -= math.Abs(operation.Payment)
	}

	// tax back
	for _, operation := range filterOperations(operations, &filterOperationsCriteria{OperationTypes: []string{"TaxBack"}}) {
		balance.Total.Currencies[string(operation.Currency)].TaxBack += math.Abs(operation.Payment)
		balance.Total.Currencies[string(operation.Currency)].BalanceAmount += math.Abs(operation.Payment)
	}

	// rounding
	for _, total := range balance.Total.Currencies {
		total.BalanceAmount = math.Round(100*total.BalanceAmount) / 100
		total.ServiceCommissionAmount = math.Round(100*total.ServiceCommissionAmount) / 100
		total.TaxBack = math.Round(100*total.TaxBack) / 100
		total.PortfolioAmount = math.Round(100*total.PortfolioAmount) / 100
	}

	PrintBalanceReport(balance)

	return balance, nil
}
