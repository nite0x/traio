import { BrowserRouter, Routes, Route } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import Layout from "./components/Layout";
import OverviewPage from "./pages/OverviewPage";
import WatchPage from "./pages/WatchPage";
import HoldingsPage from "./pages/HoldingsPage";
import BrokerPage from "./pages/BrokerPage";
import SettingsPage from "./pages/SettingsPage";
import AnalysisPage from "./pages/AnalysisPage";
import TodayPage from "./pages/TodayPage";
import ChartPage from "./pages/ChartPage";

const qc = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, staleTime: 5_000 },
  },
});

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<OverviewPage />} />
            <Route path="today" element={<TodayPage />} />
            <Route path="watch" element={<WatchPage />} />
            <Route path="chart/:symbol" element={<ChartPage />} />
            <Route path="holdings" element={<HoldingsPage />} />
            <Route path="analysis" element={<AnalysisPage />} />
            <Route path="broker" element={<BrokerPage />} />
            <Route path="settings" element={<SettingsPage />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
