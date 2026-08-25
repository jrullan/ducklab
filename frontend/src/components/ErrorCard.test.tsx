import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { ApiError } from "../api/client";
import { ErrorCard } from "./ErrorCard";
import { Skills } from "../views/Skills";

const failure = () => new ApiError(
  "it is older than this app. Restart the engine.",
  404,
  undefined,
  "GET",
  "/v1/skills",
);

describe("ErrorCard", () => {
  it("puts the useful sentence first and diagnostics behind details", () => {
    render(<ErrorCard error={failure()} />);

    expect(screen.getByText("it is older than this app. Restart the engine.")).toBeInTheDocument();
    expect(screen.queryByText(/ApiError:/)).not.toBeInTheDocument();
    const sentence = screen.getByText("it is older than this app. Restart the engine.");
    expect(sentence.tagName).toBe("P");
    const details = screen.getByText("details").closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
    expect(details).toHaveTextContent("GET /v1/skills");
    expect(details).toHaveTextContent("404");
  });

  it("does not show Loading when the request fails", async () => {
    const client = {
      skills: () => Promise.reject(failure()),
    } as never;

    render(<Skills client={client} projectId="project" />);
    await waitFor(() => expect(screen.getByTestId("skills-error")).toBeInTheDocument());
    expect(screen.queryByText("Loading…")).not.toBeInTheDocument();
  });
});
