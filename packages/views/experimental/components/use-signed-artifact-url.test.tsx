import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";

// Keep the real parseWithFallback (and the rest of the barrel) and override
// only api.rawRequest + api.getBaseUrl so signing behaviour can be driven
// deterministically.
const mockRawRequest = vi.fn();
const getBaseUrlMock = vi.fn(() => "http://localhost:8090");

vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>(
    "@multica/core/api",
  );
  return {
    ...actual,
    api: {
      rawRequest: (...args: unknown[]) => mockRawRequest(...args),
      getBaseUrl: () => getBaseUrlMock(),
    },
  };
});

import { useSignedArtifactUrl } from "./use-signed-artifact-url";

function makeResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
    text: async () => (typeof body === "string" ? body : JSON.stringify(body)),
  } as Response;
}

beforeEach(() => {
  vi.clearAllMocks();
  getBaseUrlMock.mockReturnValue("http://localhost:8090");
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useSignedArtifactUrl", () => {
  it("signs a relative artifact URL when slug and artifactId are present", async () => {
    const signedPath =
      "/api/user-plugins/demo-sign/artifacts/abc-sign/raw?sig=s&exp=1&uid=u";
    mockRawRequest.mockResolvedValueOnce(
      makeResponse(200, { url: signedPath, exp: 9999999999 }),
    );

    const { result } = renderHook(() =>
      useSignedArtifactUrl(
        "/api/user-plugins/demo-sign/artifacts/abc-sign/raw",
        "demo-sign",
        "abc-sign",
      ),
    );

    await waitFor(() =>
      expect(mockRawRequest).toHaveBeenCalledWith(
        "/api/user-plugins/demo-sign/artifacts/abc-sign/sign",
        { method: "POST" },
      ),
    );
    await waitFor(() =>
      expect(result.current).toBe(`http://localhost:8090${signedPath}`),
    );
  });

  it("does not sign when slug is empty", () => {
    const { result } = renderHook(() =>
      useSignedArtifactUrl(
        "/api/user-plugins/demo-empty/artifacts/abc-empty/raw",
        "",
        "abc-empty",
      ),
    );

    expect(mockRawRequest).not.toHaveBeenCalled();
    expect(result.current).toBe(
      "http://localhost:8090/api/user-plugins/demo-empty/artifacts/abc-empty/raw",
    );
  });

  it("does not sign an absolute URL", () => {
    const { result } = renderHook(() =>
      useSignedArtifactUrl("https://cdn.example.test/a.png", "demo-abs", "abc-abs"),
    );

    expect(mockRawRequest).not.toHaveBeenCalled();
    expect(result.current).toBe("https://cdn.example.test/a.png");
  });

  it("does not sign a data: URL", () => {
    const { result } = renderHook(() =>
      useSignedArtifactUrl("data:image/png;base64,AAAA", "demo-data", "abc-data"),
    );

    expect(mockRawRequest).not.toHaveBeenCalled();
    expect(result.current).toBe("data:image/png;base64,AAAA");
  });

  it("falls back to the unsigned URL when signing fails", async () => {
    mockRawRequest.mockRejectedValueOnce(new Error("boom"));

    const { result } = renderHook(() =>
      useSignedArtifactUrl(
        "/api/user-plugins/demo-fail/artifacts/abc-fail/raw",
        "demo-fail",
        "abc-fail",
      ),
    );

    await waitFor(() =>
      expect(result.current).toBe(
        "http://localhost:8090/api/user-plugins/demo-fail/artifacts/abc-fail/raw",
      ),
    );
  });
});
