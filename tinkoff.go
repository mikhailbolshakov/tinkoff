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

/*
func errorHandle(err error) error {
	if err == nil {
		return nil
	}

	if tradingErr, ok := err.(sdk.TradingError); ok {
		if tradingErr.InvalidTokenSpace() {
			tradingErr.Hint = "Do you use sandbox token in production environment or vise verse?"
			return tradingErr
		}
	}

	return err
}

func rest() {
	client := sdk.NewRestClient(token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение всех брокерских счетов")
	accounts, err := client.Accounts(ctx)
	if err != nil {
		log.Fatalln(errorHandle(err))
	}
	log.Printf("%+v\n", accounts)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение валютных инструментов")
	// Например: USD000UTSTOM - USD, EUR_RUB__TOM - EUR
	currencies, err := client.Currencies(ctx)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", currencies)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение фондовых инструментов")
	// Например: FXMM - Казначейские облигации США, FXGD - золото
	etfs, err := client.ETFs(ctx)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", etfs)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение облигационных инструментов")
	// Например: SU24019RMFS0 - ОФЗ 24019
	bonds, err := client.Bonds(ctx)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", bonds)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение акционных инструментов")
	// Например: SBUX - Starbucks Corporation
	stocks, err := client.Stocks(ctx)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", stocks)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение инструменов по тикеру TCS")
	// Получение инструмента по тикеру, возвращает массив инструментов потому что тикер уникален только в рамках одной биржи
	// но может совпадать на разных биржах у разных кампаний
	// Например: https://www.moex.com/ru/issue.aspx?code=FIVE и https://www.nasdaq.com/market-activity/stocks/FIVE
	// В этом примере получить нужную компанию можно проверив поле Currency
	instruments, err := client.SearchInstrumentByTicker(ctx, "TCS")
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", instruments)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение инструмента по FIGI BBG005DXJS36 (TCS)")
	// Получение инструмента по FIGI(https://en.wikipedia.org/wiki/Financial_Instrument_Global_Identifier)
	// Узнать FIGI нужного инструмента можно методами указанными выше
	// Например: BBG000B9XRY4 - Apple, BBG005DXJS36 - Tinkoff
	instrument, err := client.SearchInstrumentByFIGI(ctx, "BBG005DXJS36")
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", instrument)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение списка операций для счета по-умолчанию за последнюю неделю по инструменту(FIGI) BBG000BJSBJ0")
	// Получение списка операций за период по конкретному инструменту(FIGI)
	// Например: ниже запрашиваются операции за последнюю неделю по инструменту NEE
	operations, err := client.Operations(ctx, sdk.DefaultAccount, time.Now().AddDate(0, 0, -7), time.Now(), "BBG000BJSBJ0")
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", operations)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение списка НЕ валютных активов портфеля для счета по-умолчанию")
	positions, err := client.PositionsPortfolio(ctx, sdk.DefaultAccount)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", positions)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение списка валютных активов портфеля для счета по-умолчанию")
	positionCurrencies, err := client.CurrenciesPortfolio(ctx, sdk.DefaultAccount)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", positionCurrencies)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение списка валютных и НЕ валютных активов портфеля для счета по-умолчанию")
	// Метод является совмещеним PositionsPortfolio и CurrenciesPortfolio
	portfolio, err := client.Portfolio(ctx, sdk.DefaultAccount)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", portfolio)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение списка выставленных заявок(ордеров) для счета по-умолчанию")
	orders, err := client.Orders(ctx, sdk.DefaultAccount)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", orders)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение часовых свечей за последние 24 часа по инструменту BBG005DXJS36 (TCS)")
	// Получение свечей(ордеров)
	// Внимание! Действуют ограничения на промежуток и доступный размер свечей за него
	// Интервал свечи и допустимый промежуток запроса:
	// - 1min [1 minute, 1 day]
	// - 2min [2 minutes, 1 day]
	// - 3min [3 minutes, 1 day]
	// - 5min [5 minutes, 1 day]
	// - 10min [10 minutes, 1 day]
	// - 15min [15 minutes, 1 day]
	// - 30min [30 minutes, 1 day]
	// - hour [1 hour, 7 days]
	// - day [1 day, 1 year]
	// - week [7 days, 2 years]
	// - month [1 month, 10 years]
	// Например получение часовых свечей за последние 24 часа по инструменту BBG005DXJS36 (TCS)
	candles, err := client.Candles(ctx, time.Now().AddDate(0, 0, -1), time.Now(), sdk.CandleInterval1Hour, "BBG005DXJS36")
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", candles)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Получение ордербука(он же стакан) глубиной 10 по инструменту BBG005DXJS36")
	// Получение ордербука(он же стакан) по инструменту
	orderbook, err := client.Orderbook(ctx, 10, "BBG005DXJS36")
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", orderbook)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Выставление рыночной заявки для счета по-умолчанию на покупку ОДНОЙ акции BBG005DXJS36 (TCS)")
	// Выставление рыночной заявки для счета по-умолчанию
	// В примере ниже выставляется заявка на покупку ОДНОЙ акции BBG005DXJS36 (TCS)
	placedOrder, err := client.MarketOrder(ctx, sdk.DefaultAccount, "BBG005DXJS36", 1, sdk.BUY)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", placedOrder)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Println("Выставление лимитной заявки для счета по-умолчанию на покупку ОДНОЙ акции BBG005DXJS36 (TCS) по цене не выше 20$")
	// Выставление лимитной заявки для счета по-умолчанию
	// В примере ниже выставляется заявка на покупку ОДНОЙ акции BBG005DXJS36 (TCS) по цене не выше 20$
	placedOrder, err = client.LimitOrder(ctx, sdk.DefaultAccount, "BBG005DXJS36", 1, sdk.BUY, 20)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("%+v\n", placedOrder)

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Printf("Отмена ранее выставленной заявки для счета по-умолчанию. %+v\n", placedOrder)
	// Отмена ранее выставленной заявки для счета по-умолчанию.
	// ID заявки возвращается в структуре PlacedLimitOrder в поле ID в запросе выставления заявки client.LimitOrder
	// или в структуре Order в поле ID в запросе получения заявок client.Orders
	err = client.OrderCancel(ctx, sdk.DefaultAccount, placedOrder.ID)
	if err != nil {
		log.Fatalln(err)
	}
}
*/
