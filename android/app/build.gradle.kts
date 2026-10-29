plugins {
    id("com.android.application")
}

android {
    namespace = "com.wintp.link"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.wintp.link"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
    testOptions {
        // Android helpers such as MimeTypeMap return null instead of throwing in JVM unit tests.
        unitTests.isReturnDefaultValues = true
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.nanohttpd:nanohttpd:2.3.1")
    implementation("androidx.documentfile:documentfile:1.0.1")
    implementation("com.google.android.gms:play-services-code-scanner:16.1.0")

    testImplementation("junit:junit:4.13.2")
}
