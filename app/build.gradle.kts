plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// The ABIs to build and package: arm64 in Termux, all three elsewhere; -Pabis=a,b overrides.
val termux = System.getenv("PREFIX")?.contains("com.termux") == true
val abis: List<String> = (findProperty("abis") as String?)?.split(",")
    ?: if (termux) listOf("arm64-v8a") else listOf("arm64-v8a", "armeabi-v7a", "x86_64")

android {
    namespace = "io.github.xtratter.yggtunnel"
    compileSdk = 35
    buildToolsVersion = "35.0.0"
    defaultConfig {
        applicationId = "io.github.xtratter.yggtunnel"
        minSdk = 26
        targetSdk = 35
        versionCode = 62
        versionName = "0.46"
        // Termux builds arm64 only (no NDK there); CI and F-Droid build all three (go/build.sh)
        ndk { abiFilters += abis }
    }
    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    packaging { jniLibs { useLegacyPackaging = true } }
}

// Native core: Go → libygg.so (see go/build.sh). Rebuilt only when go/ changes.
// -PskipGo: the libraries were built beforehand (F-Droid builds Go in its own step).
val buildGo by tasks.registering(Exec::class) {
    val goDir = rootProject.file("go")
    onlyIf { !project.hasProperty("skipGo") }
    // *.sh too: server.sh and the other scripts are embedded into the library (go:embed)
    inputs.files(fileTree(goDir) { include("*.go", "*.sh", "go.mod", "go.sum", "third_party/**/*.go") })
    abis.forEach { outputs.file("src/main/jniLibs/$it/libygg.so") }
    commandLine(listOf("bash", goDir.resolve("build.sh").path) + abis)
}
tasks.named("preBuild") { dependsOn(buildGo) }

dependencies {
    testImplementation("junit:junit:4.13.2")
    // the real org.json for JVM unit tests (android.jar has only stubs)
    testImplementation("org.json:json:20240303")
}
