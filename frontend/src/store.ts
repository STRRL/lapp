import { ConnectError } from "@connectrpc/connect";
import { anyUnpack } from "@bufbuild/protobuf/wkt";
import { create } from "zustand";
import { workspaceClient } from "./api";
import {
  DiscoveryRun,
  DiscoveryRunMetadataSchema,
  DiscoveryRunState,
  LogFile,
  Pattern,
  UnmatchedErrorLine,
  Workspace
} from "./gen/lapp/web/v1/web_pb";

export type Tab = "logs" | "patterns" | "errors";

type AppState = {
  workspaces: Workspace[];
  selectedWorkspaceName: string;
  logFiles: LogFile[];
  discoveryRuns: DiscoveryRun[];
  selectedRunName: string;
  selectedPatternName: string;
  patterns: Pattern[];
  errorPatterns: Pattern[];
  unmatchedErrors: UnmatchedErrorLine[];
  activeTab: Tab;
  newWorkspaceID: string;
  notice: string;
  busy: boolean;
};

type AppActions = {
  setActiveTab: (tab: Tab) => void;
  setNewWorkspaceID: (value: string) => void;
  selectWorkspace: (name: string) => void;
  selectRun: (name: string) => void;
  selectPattern: (name: string) => void;
  runAction: (action: () => Promise<void>) => Promise<void>;
  refreshWorkspaces: () => Promise<void>;
  refreshWorkspaceDetails: (workspaceName?: string) => Promise<void>;
  refreshResults: (runName?: string) => Promise<void>;
  refreshSelectedWorkspace: () => Promise<void>;
  createWorkspace: () => Promise<void>;
  deleteWorkspace: (workspace: Workspace) => Promise<void>;
  uploadFiles: (files: FileList | null) => Promise<void>;
  deleteLogFile: (logFile: LogFile) => Promise<void>;
  startDiscovery: () => Promise<void>;
};

const emptyResults = {
  patterns: [],
  errorPatterns: [],
  unmatchedErrors: []
};

export const useAppStore = create<AppState & AppActions>()((set, get) => ({
  workspaces: [],
  selectedWorkspaceName: "",
  logFiles: [],
  discoveryRuns: [],
  selectedRunName: "",
  selectedPatternName: "",
  patterns: [],
  errorPatterns: [],
  unmatchedErrors: [],
  activeTab: "logs",
  newWorkspaceID: "",
  notice: "",
  busy: false,

  setActiveTab: (activeTab) => set({ activeTab }),
  setNewWorkspaceID: (newWorkspaceID) => set({ newWorkspaceID }),
  selectWorkspace: (selectedWorkspaceName) =>
    set({
      selectedWorkspaceName,
      selectedRunName: "",
      selectedPatternName: "",
      logFiles: [],
      discoveryRuns: [],
      ...emptyResults
    }),
  selectRun: (selectedRunName) => set({ selectedRunName, selectedPatternName: "" }),
  selectPattern: (selectedPatternName) => set({ selectedPatternName }),

  runAction: async (action) => {
    set({ busy: true, notice: "" });
    try {
      await action();
    } catch (error) {
      set({ notice: errorMessage(error) });
    } finally {
      set({ busy: false });
    }
  },

  refreshWorkspaces: async () => {
    const response = await workspaceClient.listWorkspaces({});
    const selected = get().selectedWorkspaceName;
    set({
      workspaces: response.workspaces,
      selectedWorkspaceName: selected || response.workspaces[0]?.name || ""
    });
  },

  refreshWorkspaceDetails: async (workspaceName = get().selectedWorkspaceName) => {
    if (!workspaceName) {
      set({
        logFiles: [],
        discoveryRuns: [],
        selectedRunName: "",
        ...emptyResults
      });
      return;
    }

    const [filesResponse, runsResponse] = await Promise.all([
      workspaceClient.listLogFiles({ parent: workspaceName }),
      workspaceClient.listDiscoveryRuns({ parent: workspaceName })
    ]);
    const currentRun = get().selectedRunName;
    const latestSuccessful =
      runsResponse.discoveryRuns.find((run) => run.state === DiscoveryRunState.SUCCEEDED)?.name || "";
    const selectedRunName =
      currentRun && runsResponse.discoveryRuns.some((run) => run.name === currentRun)
        ? currentRun
        : latestSuccessful;

    set({
      logFiles: filesResponse.logFiles,
      discoveryRuns: runsResponse.discoveryRuns,
      selectedRunName
    });
  },

  refreshResults: async (runName = get().selectedRunName) => {
    if (!runName) {
      set(emptyResults);
      return;
    }

    const [patternsResponse, errorsResponse] = await Promise.all([
      workspaceClient.listPatterns({ parent: runName }),
      workspaceClient.getErrorsView({ parent: runName })
    ]);
    set({
      patterns: patternsResponse.patterns,
      errorPatterns: errorsResponse.errorPatterns.map((entry) => entry.pattern).filter(isPattern),
      unmatchedErrors: errorsResponse.unmatchedErrorLines,
      selectedPatternName:
        get().selectedPatternName && patternsResponse.patterns.some((pattern) => pattern.name === get().selectedPatternName)
          ? get().selectedPatternName
          : ""
    });
  },

  refreshSelectedWorkspace: async () => {
    await get().refreshWorkspaceDetails();
    await get().refreshResults();
    await get().refreshWorkspaces();
  },

  createWorkspace: async () => {
    const workspaceId = get().newWorkspaceID.trim();
    if (!workspaceId) return;

    const response = await workspaceClient.createWorkspace({ workspaceId });
    const selectedWorkspaceName = response.workspace?.name || "";
    set({
      newWorkspaceID: "",
      selectedWorkspaceName,
      selectedRunName: "",
      selectedPatternName: "",
      ...emptyResults
    });
    await get().refreshWorkspaces();
    await get().refreshWorkspaceDetails(selectedWorkspaceName);
  },

  deleteWorkspace: async (workspace) => {
    if (!window.confirm(`Permanently delete ${workspace.workspaceId}?`)) return;

    await workspaceClient.deleteWorkspace({ name: workspace.name });
    set({
      selectedWorkspaceName: "",
      selectedRunName: "",
      selectedPatternName: "",
      logFiles: [],
      discoveryRuns: [],
      ...emptyResults
    });
    await get().refreshWorkspaces();
  },

  uploadFiles: async (files) => {
    const selectedWorkspaceName = get().selectedWorkspaceName;
    if (!files || !selectedWorkspaceName) return;

    for (const file of Array.from(files)) {
      await workspaceClient.uploadLogFile({
        parent: selectedWorkspaceName,
        fileName: file.name,
        content: new Uint8Array(await file.arrayBuffer())
      });
    }

    await get().refreshWorkspaceDetails(selectedWorkspaceName);
    await get().refreshWorkspaces();
  },

  deleteLogFile: async (logFile) => {
    if (!window.confirm(`Delete ${logFile.fileName}?`)) return;

    await workspaceClient.deleteLogFile({ name: logFile.name });
    await get().refreshWorkspaceDetails();
    await get().refreshWorkspaces();
  },

  startDiscovery: async () => {
    const selectedWorkspaceName = get().selectedWorkspaceName;
    if (!selectedWorkspaceName) return;

    const response = await workspaceClient.createDiscoveryRun({ parent: selectedWorkspaceName });
    const metadata = response.metadata ? anyUnpack(response.metadata, DiscoveryRunMetadataSchema) : undefined;
    set({ selectedRunName: metadata?.discoveryRun?.name || "" });
    await get().refreshWorkspaceDetails(selectedWorkspaceName);
    await get().refreshWorkspaces();
  }
}));

function errorMessage(error: unknown) {
  if (error instanceof ConnectError) {
    return error.rawMessage;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Unexpected error";
}

function isPattern(value: Pattern | undefined): value is Pattern {
  return value !== undefined;
}
