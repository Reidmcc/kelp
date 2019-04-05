package plugins

import (
	"log"

	"github.com/stellar/kelp/api"
	"github.com/stellar/kelp/model"
)

// staticSpreadLevelProvider provides a fixed number of levels using a static percentage spread
type dynamicSpreadLevelProvider struct {
	staticLevels      []staticLevel
	amountOfBase      float64
	offset            rateOffset
	pf                *api.FeedPair
	orderConstraints  *model.OrderConstraints
	sideAction        model.OrderAction
	carryBackTrigger  float64
	lowBalanceTarget  float64
	highBalanceTarget float64
	amountReduce      float64
	lastCounterFill   *model.Number
	counterHasFilled  bool
}

// ensure it implements the LevelProvider interface
var _ api.LevelProvider = &dynamicSpreadLevelProvider{}

// makeStaticSpreadLevelProvider is a factory method
func makeDynamicSpreadLevelProvider(
	staticLevels []staticLevel,
	amountOfBase float64,
	offset rateOffset,
	pf *api.FeedPair,
	orderConstraints *model.OrderConstraints,
	sideAction model.OrderAction,
	carryBackTrigger float64,
	lowBalanceTarget float64,
	highBalanceTarget float64,
	amountReduce float64) api.LevelProvider {
	return &dynamicSpreadLevelProvider{
		staticLevels:      staticLevels,
		amountOfBase:      amountOfBase,
		offset:            offset,
		pf:                pf,
		orderConstraints:  orderConstraints,
		sideAction:        sideAction,
		carryBackTrigger:  carryBackTrigger,
		lowBalanceTarget:  lowBalanceTarget,
		highBalanceTarget: highBalanceTarget,
		amountReduce:      amountReduce,
		lastCounterFill:   nil,
	}
}

// GetLevels impl.
func (p *dynamicSpreadLevelProvider) GetLevels(maxAssetBase float64, maxAssetQuote float64) ([]api.Level, error) {
	centerPrice, e := p.pf.GetCenterPrice()
	if e != nil {
		log.Printf("error: center price couldn't be loaded! | %s\n", e)
		return nil, e
	}
	if p.offset.percent != 0.0 || p.offset.absolute != 0 {
		// if inverted, we want to invert before we compute the adjusted price, and then invert back
		if p.offset.invert {
			centerPrice = 1 / centerPrice
		}
		scaleFactor := 1 + p.offset.percent
		if p.offset.percentFirst {
			centerPrice = (centerPrice * scaleFactor) + p.offset.absolute
		} else {
			centerPrice = (centerPrice + p.offset.absolute) * scaleFactor
		}
		if p.offset.invert {
			centerPrice = 1 / centerPrice
		}
		log.Printf("center price (adjusted): %.7f\n", centerPrice)
	}

	levels := []api.Level{}
	carryOver := 0.0
	for _, sl := range p.staticLevels {
		absoluteSpread := centerPrice * sl.SPREAD
		targetPrice := centerPrice + absoluteSpread
		targetAmount := sl.AMOUNT * p.amountOfBase
		if p.sideAction.IsSell() && maxAssetBase < p.lowBalanceTarget {
			targetAmount -= p.amountReduce * p.amountOfBase
		}
		if p.sideAction.IsBuy() && maxAssetQuote > p.highBalanceTarget {
			targetAmount -= p.amountReduce * p.amountOfBase
		}
		if p.sideAction.IsBuy() && p.lastCounterFill != nil {
			absoluteLimit := (1 / centerPrice) * p.carryBackTrigger
			// log.Printf("absolute limit calculated as: %v\n", absoluteLimit)
			// log.Printf("testing against %v\n", p.lastCounterFill.AsFloat()-absoluteLimit)
			if 1/targetPrice > p.lastCounterFill.AsFloat()-absoluteLimit {
				carryOver += targetAmount
				log.Printf("Carrying over %v due to price move\n", targetAmount)
				log.Printf("Last counter fill was %v, target price was %v\n", p.lastCounterFill, targetPrice)
				continue
			}
		}
		if p.sideAction.IsSell() && p.lastCounterFill != nil {
			absoluteLimit := centerPrice * p.carryBackTrigger
			// log.Printf("absolute limit calculated as: %v\n", absoluteLimit)
			// log.Printf("testing against %v\n", p.lastCounterFill.AsFloat()+absoluteLimit)
			// absoluteLimit := centerPrice * (1 - p.counterLimit)
			if targetPrice < p.lastCounterFill.AsFloat()+absoluteLimit {
				carryOver += targetAmount
				log.Printf("Carrying over %v due to price move\n", targetAmount)
				log.Printf("Last counter fill was %v, target price was %v\n", p.lastCounterFill, targetPrice)
				continue
			}
		}

		levels = append(levels, api.Level{
			// we always add here because it is only used in the context of selling so we always charge a higher price to include a spread
			Price:  *model.NumberFromFloat(targetPrice, p.orderConstraints.PricePrecision),
			Amount: *model.NumberFromFloat(targetAmount+carryOver, p.orderConstraints.VolumePrecision),
		})
		carryOver = 0.0
	}
	return levels, nil
}

// GetFillHandlers impl
func (p *dynamicSpreadLevelProvider) GetFillHandlers() ([]api.FillHandler, error) {
	return []api.FillHandler{p}, nil
}

// HandleFill impl
func (p *dynamicSpreadLevelProvider) HandleFill(trade model.Trade) error {
	if trade.OrderAction.IsBuy() != p.sideAction.IsBuy() {
		p.lastCounterFill = trade.Price
	}
	return nil
}
