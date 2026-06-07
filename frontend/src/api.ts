import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { WorkspaceService } from "./gen/lapp/web/v1/web_pb";

const transport = createConnectTransport({
  baseUrl: window.location.origin,
  useBinaryFormat: false
});

export const workspaceClient = createClient(WorkspaceService, transport);
