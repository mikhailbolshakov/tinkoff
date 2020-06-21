package tinkoff

import (
	"os"

	"github.com/jedib0t/go-pretty/table"
)

func PrintBalanceReport(request *TcfPortfolioBalance) {

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"FIGI",
		"Ticker",
		"Name",
		"Currency",
		"Balance",
		"Commission",
		"Portfolio",
		"Dividend",
		"Service commission",
		"Tax back"})

	for _, row := range request.Items {
		t.AppendRow([]interface{}{
			row.FIGI,
			row.Ticker,
			row.Name,
			row.Currency,
			row.BalanceAmount,
			row.BrokerCommissionAmount,
			row.PortfolioAmount,
			row.DividendAmount - row.DividendTaxAmount,
			"",
			"",
		})
	}

	for currency, total := range request.Total.Currencies {
		t.AppendFooter([]interface{}{
			"",
			"",
			"Total",
			currency,
			total.BalanceAmount,
			"",
			total.PortfolioAmount,
			"",
			total.ServiceCommissionAmount,
			total.TaxBack,
		})
	}

	t.Render()
}
