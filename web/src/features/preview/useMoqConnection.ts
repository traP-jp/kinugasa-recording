import * as Watch from "@moq/watch";
import { useEffect, useMemo, useState } from "react";
import type { PreviewAccess } from "../../api/types";
import { createMoqConnectionUrl } from "../../lib/moq";

type ConnectionStatus = Watch.Net.Connection.Status | "unsupported" | "invalid";

interface MoqConnectionState {
  connection?: Watch.Net.Connection;
  status: ConnectionStatus;
  failed: boolean;
}

export function useMoqConnection(access: PreviewAccess): MoqConnectionState {
  const connectionUrl = useMemo(() => {
    try {
      return createMoqConnectionUrl(access);
    } catch {
      return undefined;
    }
  }, [access.accessToken, access.url]);
  const [state, setState] = useState<MoqConnectionState>({
    status: connectionUrl ? "connecting" : "invalid",
    failed: false,
  });

  useEffect(() => {
    if (!connectionUrl) {
      setState({ status: "invalid", failed: true });
      return;
    }
    if (!Watch.Net.Connection.isWebTransportSupported()) {
      setState({ status: "unsupported", failed: true });
      return;
    }

    const connection = new Watch.Net.Connection({
      url: connectionUrl,
      discovery: true,
      websocket: { enabled: false },
    });
    const update = () => {
      setState({
        connection,
        status: connection.status.peek(),
        failed: connection.error.peek() !== undefined,
      });
    };
    update();
    const unsubscribeStatus = connection.status.subscribe(update);
    const unsubscribeError = connection.error.subscribe(update);

    return () => {
      unsubscribeStatus();
      unsubscribeError();
      connection.close();
    };
  }, [connectionUrl]);

  return state;
}
