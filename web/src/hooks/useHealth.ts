import { useEffect, useState } from "react";

type HealthResponse = {
  status?: string;
};

type UseHealthResult = {
  status: string | null;
  isLoading: boolean;
  error: string | null;
};

export default function useHealth(): UseHealthResult {
  const [status, setStatus] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let isActive = true;

    const fetchHealth = async () => {
      setIsLoading(true);
      setError(null);

      try {
        const response = await fetch("/health");
        if (!response.ok) {
          throw new Error(`Request failed with status ${response.status}`);
        }

        const data = (await response.json()) as HealthResponse;
        if (isActive) {
          setStatus(data.status ?? "unknown");
        }
      } catch (err) {
        if (isActive) {
          const message = err instanceof Error ? err.message : "Unknown error";
          setError(message);
        }
      } finally {
        if (isActive) {
          setIsLoading(false);
        }
      }
    };

    fetchHealth();

    return () => {
      isActive = false;
    };
  }, []);

  return { status, isLoading, error };
}
