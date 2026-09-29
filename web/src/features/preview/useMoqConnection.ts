import * as Moq from "@moq/net";
import { useEffect, useMemo, useState } from "react";
import type { PreviewAccess } from "../../api/types";
import {
  createMoqConnectionUrl,
  createMoqServerCertificateHashes,
} from "../../lib/moq";

type ConnectionStatus = Moq.Connection.Status | "unsupported" | "invalid";

interface MoqConnectionState {
  connection?: Moq.Connection;
  status: ConnectionStatus;
  failed: boolean;
}

export function useMoqConnection(access: PreviewAccess): MoqConnectionState {
  const connectionOptions = useMemo(() => {
    try {
      return {
        url: createMoqConnectionUrl(access),
        serverCertificateHashes: createMoqServerCertificateHashes(access),
      };
    } catch {
      return undefined;
    }
  }, [access.accessToken, access.serverCertificateHashes, access.url]);
  const [state, setState] = useState<MoqConnectionState>({
    status: connectionOptions ? "connecting" : "invalid",
    failed: false,
  });

  useEffect(() => {
    if (!connectionOptions) {
      setState({ status: "invalid", failed: true });
      return;
    }
    if (!Moq.Connection.isWebTransportSupported()) {
      setState({ status: "unsupported", failed: true });
      return;
    }

    const connection = new Moq.Connection({
      url: connectionOptions.url,
      discovery: true,
      webtransport: {
        serverCertificateHashes: connectionOptions.serverCertificateHashes,
      },
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
  }, [connectionOptions]);

  return state;
}
