import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, Quote } from "../api/client";

interface QuoteMessage {
  type: "quote";
  quote: Quote;
}

function mergeQuote(quotes: Quote[] | undefined, update: Quote): Quote[] | undefined {
  if (!quotes) return quotes;
  const index = quotes.findIndex((quote) => quote.symbol === update.symbol);
  if (index < 0) return quotes;
  const next = [...quotes];
  next[index] = { ...next[index], ...update };
  return next;
}

export function useLiveQuotes(symbols: string[]) {
  const queryClient = useQueryClient();
  const key = [...new Set(symbols.map((symbol) => symbol.trim().toUpperCase()).filter(Boolean))]
    .sort()
    .join(",");

  useEffect(() => {
    if (!key) return;
    let socket: WebSocket | null = null;
    let retry: ReturnType<typeof setTimeout> | null = null;
    let closed = false;

    const connect = () => {
      socket = new WebSocket(api.quoteStreamUrl(key.split(",")));
      socket.onmessage = (event) => {
        const message = JSON.parse(event.data) as QuoteMessage | { type: string };
        if (message.type !== "quote") return;
        const quote = (message as QuoteMessage).quote;
        queryClient.setQueriesData<Quote[]>({ queryKey: ["quotes"] }, (old) =>
          mergeQuote(old, quote)
        );
        queryClient.setQueriesData<Quote[]>({ queryKey: ["quotes-symbols"] }, (old) =>
          mergeQuote(old, quote)
        );
        queryClient.setQueryData<Quote>(["quote-symbol", quote.symbol], (old) => ({
          ...old,
          ...quote,
        }));
      };
      socket.onclose = () => {
        if (!closed) retry = setTimeout(connect, 2_000);
      };
    };

    connect();
    return () => {
      closed = true;
      if (retry) clearTimeout(retry);
      socket?.close();
    };
  }, [key, queryClient]);
}
