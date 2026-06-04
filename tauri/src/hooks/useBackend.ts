import { useEffect, useState } from "react";
import { api } from "../api/client";

type Status = "connecting" | "online" | "offline";

export function useBackendStatus(pollMs = 5000) {
  const [status, setStatus] = useState<Status>("connecting");

  useEffect(() => {
    let cancelled = false;

    const check = async () => {
      try {
        await api.health();
        if (!cancelled) setStatus("online");
      } catch {
        if (!cancelled) setStatus("offline");
      }
    };

    check();
    const id = setInterval(check, pollMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [pollMs]);

  return status;
}
