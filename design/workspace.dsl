workspace {
    name "Meta Cost Center"
    description "C4 model for the macOS arm64 port"

    model {
        participant = person "Hackathon participant" "Uses Meta Cost Center to understand local Meta API costs."
        costCenter = softwareSystem "Meta Cost Center" "Local Go application that imports Muse usage, estimates cost, and presents reports."
        muse = softwareSystem "Muse Code" "Local coding agent that writes session records."
        sessionLogs = softwareSystem "Muse session logs" "Local JSONL session records."
        database = softwareSystem "Cost Center SQLite database" "Local normalized usage and configuration data."
        browser = softwareSystem "Web browser" "Displays the local dashboard."
        operatingSystem = softwareSystem "Operating system" "Linux or macOS; provides filesystem, terminal, and process APIs."

        participant -> costCenter "Uses CLI, TUI, and dashboard"
        muse -> sessionLogs "Writes session records"
        costCenter -> sessionLogs "Reads usage records"
        costCenter -> database "Stores normalized usage"
        browser -> costCenter "Calls localhost HTTP dashboard"
        costCenter -> operatingSystem "Uses filesystem, terminal, and process APIs"
    }

    views {
        systemContext costCenter "SystemContext" {
            include *
            autoLayout lr
            title "Meta Cost Center - System Context"
            description "The application remains in user space; Linux/macOS differences are at the operating-system boundary."
        }

        styles {
            element "Person" {
                background #08427B
                color #FFFFFF
                shape Person
            }
            element "Software System" {
                background #1168BD
                color #FFFFFF
            }
        }
    }
}
