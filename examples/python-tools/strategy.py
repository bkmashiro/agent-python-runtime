# pyright: reportUndefinedVariable=false
# `inputs` and `market` are injected by Pysolate for this Run.
symbols = inputs["symbols"]
quantities = inputs["quantities"]

quotes = [market.get_price(symbol=symbol) for symbol in symbols]
leader = max(quotes, key=lambda quote: quote["price"])
portfolio_value = sum(
    quote["price"] * quantities[quote["symbol"]]
    for quote in quotes
)

result = {
    "leader": leader,
    "portfolio_value": round(portfolio_value, 2),
    "quotes": quotes,
}
