#pragma once

#include <utility>
#include <array>

/// Topic and service names for the demo device API.
///
/// Every language binding reads these strings from here, so a rename is a
/// one-line change rather than a grep across the tree.
namespace demo::names {

// --- Topics ------------------------------------------------------------

// Periodic device state.
inline constexpr const char* kStateTopic = "demo/state";
inline constexpr const char* kReadingsTopic = "demo/readings";

// --- Services ----------------------------------------------------------

inline constexpr const char* kSetModeService = "demo/set_mode";
inline constexpr const char* kResetAllService = "demo/reset/all";

// Reset scopes, keyed by how much state each one clears.
inline constexpr std::array<std::pair<const char*, const char*>, 3> kResetTargets = {{
    {"soft", "demo/reset/soft"},
    {"hard", "demo/reset/hard"},
    {"default", kResetAllService},
}};

}  // namespace demo::names
