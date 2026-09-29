import * as Moq from "@moq/net";
import mpegts from "mpegts.js";

export const MPEGTS_TRACK = "mpegts";

// mpegts.js expects a byte stream. Each MoQ frame contains one unmodified RIST
// payload; the ordered reader restores group and frame order before passing bytes to it.
export function createMoqTsLoader(
  connection: Moq.Connection,
  cameraName: string,
): NonNullable<NonNullable<Parameters<typeof mpegts.createPlayer>[1]>["customLoader"]> {
  return class MoqTsLoader extends mpegts.BaseLoader {
    private request?: Moq.Origin.Requesting;
    private unsubscribeOrigin?: () => void;
    private unsubscribeBroadcast?: () => void;
    private reader?: Moq.Track.Ordered;
    private generation = 0;
    private receivedLength = 0;

    constructor() {
      super("moq-mpegts-loader");
      this._needStash = false;
    }

    open(): void {
      this.abort();
      this.receivedLength = 0;
      this._status = mpegts.LoaderStatus.kConnecting;
      const attach = (broadcast: Moq.Broadcast.Consumer | undefined) => {
        this.reader?.close();
        this.reader = undefined;
        const generation = ++this.generation;
        if (!broadcast) return;
        const reader = broadcast.track(MPEGTS_TRACK).subscribe({ maxAge: Moq.Time.Milli(5_000) }).ordered();
        this.reader = reader;
        void this.read(reader, generation);
      };
      const attachOrigin = (origin: Moq.Origin.Table | undefined) => {
        this.unsubscribeBroadcast?.();
        this.unsubscribeBroadcast = undefined;
        this.request?.close();
        this.request = undefined;
        attach(undefined);
        if (!origin) return;
        const request = origin.request(Moq.Path.from(cameraName), { announced: true });
        this.request = request;
        this.unsubscribeBroadcast = request.active.subscribe(attach);
        attach(request.active.peek());
      };
      this.unsubscribeOrigin = connection.origin.subscribe(attachOrigin);
      attachOrigin(connection.origin.peek());
    }

    private async read(reader: Moq.Track.Ordered, generation: number): Promise<void> {
      try {
        for (;;) {
          const frame = await reader.readFrame();
          if (!frame || generation !== this.generation) break;
          this._status = mpegts.LoaderStatus.kBuffering;
          const bytes = frame.payload;
          const chunk = new Uint8Array(bytes).buffer;
          const start = this.receivedLength;
          this.receivedLength += chunk.byteLength;
          this.onDataArrival?.(chunk, start, this.receivedLength);
        }
      } catch (error) {
        if (generation === this.generation) {
          this._status = mpegts.LoaderStatus.kError;
          // mpegts.js declares this argument as the whole LoaderErrors object,
          // although its runtime callback takes one of the string values.
          this.onError?.(mpegts.LoaderErrors.EXCEPTION as never, { code: -1, msg: String(error) });
        }
      }
    }

    abort(): void {
      ++this.generation;
      this.unsubscribeOrigin?.();
      this.unsubscribeOrigin = undefined;
      this.unsubscribeBroadcast?.();
      this.unsubscribeBroadcast = undefined;
      this.reader?.close();
      this.reader = undefined;
      this.request?.close();
      this.request = undefined;
      this._status = mpegts.LoaderStatus.kComplete;
    }

    destroy(): void {
      this.abort();
      super.destroy();
    }
  };
}
