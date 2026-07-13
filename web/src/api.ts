export interface SessionResource {
  name: string;
}

interface SessionResponse {
  session: SessionResource;
}

interface ErrorResponse {
  error?: {
    code?: string;
    message?: string;
  };
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export async function createSession(name: string): Promise<SessionResource> {
  const response = await fetch("/api/v1/sessions", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ name }),
  });
  const body = (await response.json()) as SessionResponse & ErrorResponse;

  if (!response.ok) {
    throw new ApiError(
      response.status,
      body.error?.code ?? "INTERNAL",
      body.error?.message ?? "Session creation failed",
    );
  }

  return body.session;
}
